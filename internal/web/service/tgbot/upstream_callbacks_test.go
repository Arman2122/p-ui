package tgbot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mymmrac/telego"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/global"
)

type capturedBotRequest struct {
	ChatID      int64 `json:"chat_id"`
	Method      string
	Document    string
	Text        string          `json:"text"`
	ReplyMarkup json.RawMessage `json:"reply_markup"`
}

func captureBotRequests(t *testing.T, apiError ...string) func() []capturedBotRequest {
	t.Helper()
	var mu sync.Mutex
	var requests []capturedBotRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request capturedBotRequest
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("decode upload: %v", err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			request.ChatID, _ = strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
			for _, files := range r.MultipartForm.File {
				for _, header := range files {
					file, err := header.Open()
					if err != nil {
						t.Errorf("open upload: %v", err)
						continue
					}
					content, err := io.ReadAll(file)
					_ = file.Close()
					if err != nil {
						t.Errorf("read upload: %v", err)
					}
					request.Document = string(content)
				}
			}
			request.ReplyMarkup = json.RawMessage(r.FormValue("reply_markup"))
		} else if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Telegram request: %v", err)
		}
		request.Method = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if len(apiError) > 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": apiError[0]})
			return
		}
		if request.Method == "answerCallbackQuery" || request.Method == "deleteMessage" {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":-991,"type":"group"}}}`)
	}))
	t.Cleanup(server.Close)
	sender, err := telego.NewBot("1234567890:aaaabbbbaaaabbbbaaaabbbbaaaabbbbccc", telego.WithAPIServer(server.URL), telego.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	tgBotMutex.Lock()
	previousBot, previousRunning := bot, isRunning
	bot, isRunning = sender, true
	tgBotMutex.Unlock()
	previousHash := hashStorage
	hashStorage = global.NewHashStorage(time.Minute)
	t.Cleanup(func() {
		tgBotMutex.Lock()
		bot, isRunning = previousBot, previousRunning
		tgBotMutex.Unlock()
		hashStorage = previousHash
	})
	return func() []capturedBotRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedBotRequest(nil), requests...)
	}
}

func TestBotWorkerRecoversAndReleasesItsSlot(t *testing.T) {
	previous := messageWorkerPool
	messageWorkerPool = make(chan struct{}, 1)
	t.Cleanup(func() { messageWorkerPool = previous })
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		runBotHandler(func() { panic("simulated bad Telegram update") })
	}()
	if escaped != nil || len(messageWorkerPool) != 0 {
		t.Fatalf("panic=%v, occupied slots=%d; want recovered and released", escaped, len(messageWorkerPool))
	}
	ran := false
	runBotHandler(func() { ran = true })
	if !ran {
		t.Fatal("next worker was not executed")
	}
}

func TestBotEditsOnlySuppressUnchangedMessageErrors(t *testing.T) {
	for _, input := range []struct {
		description string
		warnings    int
	}{
		{"Bad Request: message is not modified", 0},
		{"Bad Request: No fields to modify", 0},
		{"Bad Request: message to edit not found", 2},
	} {
		t.Run(input.description, func(t *testing.T) {
			requests := captureBotRequests(t, input.description)
			count := func() int {
				n := 0
				for _, line := range logger.GetLogs(10000, "warning") {
					if strings.Contains(line, input.description) {
						n++
					}
				}
				return n
			}
			before := count()
			tg := &Tgbot{}
			tg.editMessageTgBot(-991, 1, "unchanged")
			tg.editMessageCallbackTgBot(-991, 1, &telego.InlineKeyboardMarkup{})
			if got := count() - before; got != input.warnings || len(requests()) != 2 {
				t.Fatalf("warnings=%d requests=%d, want %d and2", got, len(requests()), input.warnings)
			}
		})
	}
}

func TestOversizedBotLinesRemainDeliverable(t *testing.T) {
	t.Run("plain UTF-8 line", func(t *testing.T) {
		requests := captureBotRequests(t)
		message := strings.Repeat("سلام", 1000)
		(&Tgbot{}).SendMsgToTgbot(-991, message)
		var combined strings.Builder
		for _, request := range requests() {
			if request.Method != "sendMessage" || len(request.Text) > 2000 || !utf8.ValidString(request.Text) {
				t.Fatalf("invalid plain-text delivery: %+v", request)
			}
			combined.WriteString(request.Text)
		}
		if combined.String() != message {
			t.Fatal("plain-text paging lost content")
		}
	})
	t.Run("formatted line becomes document", func(t *testing.T) {
		requests := captureBotRequests(t)
		message := "<code>" + strings.Repeat("vless://long-link", 350) + "</code>"
		(&Tgbot{}).SendMsgToTgbot(-991, message)
		got := requests()
		if len(got) != 1 || got[0].Method != "sendDocument" || got[0].Document != message {
			t.Fatalf("formatted delivery = %+v, want one exact document", got)
		}
	})
	t.Run("multiline formatted field stays intact", func(t *testing.T) {
		requests := captureBotRequests(t)
		message := "IP history:\n<code>" + strings.Repeat("2001:db8::1\n", 250) + "</code>"
		(&Tgbot{}).SendMsgToTgbot(-991, message)
		got := requests()
		if len(got) != 1 || got[0].Method != "sendDocument" || got[0].Document != message {
			t.Fatalf("multiline formatted delivery count=%d, want one exact document", len(got))
		}
	})
}

func TestWizardCallbacksKeepEachAdministratorsDraft(t *testing.T) {
	requests := captureBotRequests(t)
	t.Cleanup(userStateMgr.reset)
	tg := &Tgbot{}
	for _, input := range []struct {
		user int64
		data string
	}{
		{11, "add_client_reset_exp_c 7"},
		{22, "add_client_reset_exp_c 14"},
		{11, "add_client_limit_traffic_c 5"},
	} {
		tg.answerCallback(&telego.CallbackQuery{ID: "wizard", From: telego.User{ID: input.user}, Data: input.data, Message: &telego.Message{Chat: telego.Chat{ID: -991}}}, true)
	}
	var edits []string
	for _, request := range requests() {
		if request.Method == "editMessageText" {
			edits = append(edits, request.Text)
		}
	}
	if len(edits) != 3 || !strings.Contains(edits[0], "Expire: 7 ") || !strings.Contains(edits[1], "Expire: 14 ") || !strings.Contains(edits[2], "Expire: 7 ") {
		t.Fatalf("wizard edits mixed administrators' expiry terms: %q", edits)
	}
	for _, step := range []struct {
		user     int64
		callback string
	}{
		{11, "add_client_ch_default_email"},
		{22, "add_client_ch_default_comment"},
	} {
		tg.answerCallback(&telego.CallbackQuery{ID: "step", From: telego.User{ID: step.user}, Data: step.callback, Message: &telego.Message{Chat: telego.Chat{ID: -991}}}, true)
	}
	if state, ok := userStateMgr.get(chatUser{chatID: -991, userID: 11}); !ok || state != "awaiting_email" {
		t.Fatalf("first administrator's next field = %q, %v, want awaiting_email", state, ok)
	}
}

func TestUnknownAndExpiredCallbacksAreAnswered(t *testing.T) {
	for _, data := range []string{"unknown", "unknown 5", "0123456789abcdef0123456789abcdef"} {
		t.Run(data, func(t *testing.T) {
			requests := captureBotRequests(t)
			(&Tgbot{}).answerCallback(&telego.CallbackQuery{ID: "unknown", From: telego.User{ID: 11}, Data: data, Message: &telego.Message{Chat: telego.Chat{ID: -991}}}, true)
			answers := 0
			for _, request := range requests() {
				if request.Method == "answerCallbackQuery" {
					answers++
				}
			}
			if answers != 1 {
				t.Fatalf("callback answers = %d, want exactly 1", answers)
			}
		})
	}
}

func TestLongBotMessagesKeepSingleNewlineBoundaries(t *testing.T) {
	for _, separator := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(separator, "\n", "LF"), func(t *testing.T) {
			requests := captureBotRequests(t)
			line := "<code>" + strings.Repeat("ب", 350) + "</code>"
			message := strings.Join([]string{line, line, line}, separator)
			(&Tgbot{}).SendMsgToTgbot(-991, message, &telego.InlineKeyboardMarkup{})
			var chunks []string
			got := requests()
			for index, request := range got {
				if request.Method != "sendMessage" {
					continue
				}
				if len(request.Text) > 2000 || !utf8.ValidString(request.Text) {
					t.Fatalf("invalid or oversized chunk: %d bytes", len(request.Text))
				}
				chunks = append(chunks, request.Text)
				if (len(request.ReplyMarkup) > 0) != (index == len(got)-1) {
					t.Fatalf("reply keyboard on chunk %d, want only the final chunk", index)
				}
			}
			if len(chunks) < 2 || strings.Join(chunks, "") != message {
				t.Fatalf("message pagination changed content: %q", chunks)
			}
		})
	}
}
