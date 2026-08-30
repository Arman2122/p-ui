package email

import (
	"bufio"
	"errors"
	"net"
	"net/smtp"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The Test SMTP button must exercise the path a real send takes.

sendPlain upgrades to STARTTLS whenever the server advertises it, whatever the
configured transport is -- there is no branch on the setting at all. TestConnection
upgraded only when the setting was literally "starttls".

So the two disagree for every other transport, and the button answers about a
connection the panel will never make. Against a relay that advertises STARTTLS
and refuses AUTH before it, "none" tests RED while sending works; against one
where the upgrade itself fails, the button tests GREEN while every send errors.
Either way the operator is told the opposite of the truth about the channel their
expiry notices and login alerts go out on.

One token: the gate mirrors sendPlain by excluding only implicit TLS, which
never probes for the extension. F352.
*/

// startTLSOnlyAuthServer speaks just enough SMTP to advertise STARTTLS and to
// refuse AUTH until the client has asked for it, which is what a hardened relay
// does. It never completes a TLS handshake -- reaching STARTTLS is the signal.
func startTLSOnlyAuthServer(t *testing.T) (addr string, sawStartTLS func() bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	reached := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				_, _ = c.Write([]byte("220 relay.example.com ESMTP\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch verb := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
						_, _ = c.Write([]byte("250-relay.example.com\r\n250-STARTTLS\r\n250 AUTH PLAIN LOGIN\r\n"))
					case strings.HasPrefix(verb, "STARTTLS"):
						select {
						case reached <- struct{}{}:
						default:
						}
						// Refuse the handshake: the client gets an error either way,
						// and what this test measures is whether it ASKED.
						_, _ = c.Write([]byte("454 TLS not available\r\n"))
						return
					case strings.HasPrefix(verb, "AUTH"):
						_, _ = c.Write([]byte("530 5.7.0 Must issue a STARTTLS command first\r\n"))
					case strings.HasPrefix(verb, "QUIT"):
						_, _ = c.Write([]byte("221 Bye\r\n"))
						return
					default:
						_, _ = c.Write([]byte("250 OK\r\n"))
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), func() bool {
		select {
		case <-reached:
			return true
		default:
			return false
		}
	}
}

func TestTestConnectionUpgradesWhereverASendWould(t *testing.T) {
	initTestDB(t)

	addr, sawStartTLS := startTLSOnlyAuthServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	db := database.GetDB()
	for key, value := range map[string]string{
		"smtpHost":           host,
		"smtpPort":           portStr,
		"smtpUsername":       "notifier@example.com",
		"smtpPassword":       "hunter2",
		"smtpFrom":           "notifier@example.com",
		"smtpTo":             "admin@example.com",
		"smtpEncryptionType": "none",
	} {
		if err := db.Save(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	res := (&EmailService{}).TestConnection()

	if !sawStartTLS() {
		t.Fatalf("the Test SMTP button never issued STARTTLS against a relay advertising it, while a real send always would; the button therefore reports on a connection the panel never makes. result=%+v", res)
	}
}

/*
Auth over a cleartext session must say what happened.

net/smtp refuses PLAIN on an unencrypted connection with the bare string
"unencrypted connection" -- no mention of SMTP, of the password, or of what to
change. Against a relay that offers no STARTTLS with credentials configured,
that is the error the operator got for every expiry notice and login alert.

Worse, they never even saw that. classifySMTPError returned keys already
prefixed with "pages.settings.", and the settings page renders
t("pages.settings." + msg) -- so every SMTP failure resolved
"pages.settings.pages.settings.smtpErrorAuth", which does not exist, and the
operator was shown the raw key. The keys are bare now and the contract is stated
above the function. F366.
*/
func TestClassifySMTPErrorReturnsABareKey(t *testing.T) {
	cases := map[string]string{
		"535 authentication failed":    "smtpErrorAuth",
		"unencrypted connection":       "smtpErrorUnencrypted",
		"dial tcp: connection refused": "smtpErrorRefused",
		"i/o timeout":                  "smtpErrorTimeout",
	}
	for input, want := range cases {
		got := classifySMTPError(errors.New(input))
		if got != want {
			t.Errorf("classifySMTPError(%q) = %q, want %q", input, got, want)
		}
		if strings.HasPrefix(got, "pages.settings.") {
			t.Errorf("classifySMTPError(%q) returned a prefixed key; the caller prefixes again, so it resolves to nothing and the operator sees the raw key", input)
		}
	}
}

// And the send path names the cause instead of passing the bare stdlib string on.
func TestSendPlainNamesACleartextAuthRefusal(t *testing.T) {
	addr, _ := plainNoStartTLSServer(t)

	/* The server NAME must not be a loopback literal: net/smtp exempts
	   localhost from the cleartext-auth refusal, so dialling 127.0.0.1 under
	   its own name would never reach the branch under test. */
	const host = "relay.example.com"
	auth := smtp.PlainAuth("", "user", "pass", host)
	err := (&EmailService{}).sendPlain(addr, auth, "from@example.com",
		[]string{"to@example.com"}, []byte("body"), host)
	if err == nil {
		t.Fatal("auth over a cleartext session succeeded; net/smtp refuses it")
	}
	if !strings.Contains(err.Error(), "cleartext") {
		t.Errorf("the error does not say the session was unencrypted, so nothing downstream can classify it: %v", err)
	}
	if classifySMTPError(err) != "smtpErrorUnencrypted" {
		t.Errorf("the wrapped error does not classify as unencrypted: %q", classifySMTPError(err))
	}
}

// A relay that advertises AUTH but not STARTTLS, which is what a plain local
// relay looks like and exactly the configuration the "none" option exists for.
func plainNoStartTLSServer(t *testing.T) (addr string, release func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				_, _ = c.Write([]byte("220 relay.example.com ESMTP\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch verb := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
						_, _ = c.Write([]byte("250-relay.example.com\r\n250 AUTH PLAIN LOGIN\r\n"))
					case strings.HasPrefix(verb, "QUIT"):
						_, _ = c.Write([]byte("221 Bye\r\n"))
						return
					default:
						_, _ = c.Write([]byte("250 OK\r\n"))
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}
