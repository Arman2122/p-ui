// Package main is the entry point for the Penhoon UI web panel application.
// It initializes the database, web server, and handles command-line operations for managing the panel.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	_ "unsafe"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/sub"
	"github.com/Arman2122/p-ui/internal/tunnelmonitor"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/util/sys"
	"github.com/Arman2122/p-ui/internal/web"
	"github.com/Arman2122/p-ui/internal/web/global"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/service/panel"
	"github.com/Arman2122/p-ui/internal/web/service/tgbot"

	"github.com/joho/godotenv"
	"github.com/op/go-logging"
)

// runWebServer initializes and starts the web server for the Penhoon UI panel.
func runWebServer() {
	role, err := config.RoleFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := role.ValidatePanel(); err != nil {
		log.Fatal(err)
	}
	log.Printf("Starting %v %v", config.GetName(), config.GetPanelVersion())

	switch config.GetLogLevel() {
	case config.Debug:
		logger.InitLogger(logging.DEBUG)
	case config.Info:
		logger.InitLogger(logging.INFO)
	case config.Notice:
		logger.InitLogger(logging.NOTICE)
	case config.Warning:
		logger.InitLogger(logging.WARNING)
	case config.Error:
		logger.InitLogger(logging.ERROR)
	default:
		log.Fatalf("Unknown log level: %v", config.GetLogLevel())
	}

	if !role.ManagementOnly() {
		for _, line := range sys.ApplyMemoryTuning() {
			logger.Info(line)
		}
	}

	if os.Getenv("PUI_PPROF") == "true" {
		go func() {
			logger.Info("pprof profiling server listening on 127.0.0.1:6060")
			if err := http.ListenAndServe("127.0.0.1:6060", nil); err != nil {
				logger.Warning("pprof server stopped: ", err)
			}
		}()
	}

	err = database.InitDB()
	if err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}
	roleCtx, stopRole := context.WithCancel(context.Background())
	defer func() { stopRole() }()
	serverOptions, err := roleServerOptions(roleCtx, role)
	if err != nil {
		log.Fatalf("Error preparing panel role: %v", err)
	}
	fleetCtx, stopFleet := context.WithCancel(context.Background())
	fleetDone := make(chan struct{})
	go func() { defer close(fleetDone); service.RunFleetControl(fleetCtx) }()
	defer func() { stopFleet(); <-fleetDone }()

	server := web.NewServer(serverOptions...)
	global.SetWebServer(server)
	err = server.Start()
	if err != nil {
		log.Fatalf("Error starting web server: %v", err)
	}

	sub.SetDistFS(web.EmbeddedDist())
	service.RegisterSubLinkProvider(sub.NewLinkProvider())
	subServer := sub.NewServer()
	global.SetSubServer(subServer)
	/*
		A subscription server that cannot bind is not a reason to kill the panel.

		This was log.Fatalf, reached AFTER the panel's own HTTP server is already
		serving — so a port conflict on the subscription port took down the one
		interface an operator would use to change that port, and did it again on
		every boot. The panel is the way back in; it stays up.
	*/
	if err = subServer.Start(); err != nil {
		logger.Errorf("[SUB_SERVER] the subscription server did not start: %v. "+
			"The panel is running and subscription links will not be served until this is fixed — "+
			"change the subscription port in Settings, or disable the subscription server there.", err)
	}

	sigCh := make(chan os.Signal, 8)
	// Trap shutdown signals
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, sys.SIGUSR1, os.Interrupt)
	global.SetRestartHook(func() {
		select {
		case sigCh <- syscall.SIGHUP:
		default:
		}
	})

	var stopTunnelHealthMonitor context.CancelFunc
	monitorCfg := tunnelmonitor.ConfigFromEnv()
	if monitorCfg.Enabled && !role.ManagementOnly() {
		if monitorCfg.ProxyURL == "" {
			logger.Warning("Tunnel health monitor enabled without PUI_TUNNEL_HEALTH_PROXY: the probe measures host connectivity, not the xray tunnel, so failures will restart xray without fixing host network issues")
		}

		monitorCtx, cancel := context.WithCancel(context.Background())
		stopTunnelHealthMonitor = cancel

		monitor, err := tunnelmonitor.New(monitorCfg, func(_ context.Context) error {
			logger.Warning("Tunnel health monitor threshold reached, restarting xray-core")
			return server.RestartXray()
		})
		if err != nil {
			logger.Warning("Tunnel health monitor disabled: ", err)
		} else {
			go monitor.Run(monitorCtx)
		}
	}
	for {
		sig := <-sigCh

		switch sig {
		case syscall.SIGHUP:
			logger.Info("Received SIGHUP signal. Restarting servers...")

			stopRole()
			err := server.StopPanelOnly()
			if err != nil {
				logger.Debug("Error stopping web server:", err)
			}
			err = subServer.Stop()
			if err != nil {
				logger.Debug("Error stopping sub server:", err)
			}

			roleCtx, stopRole = context.WithCancel(context.Background())
			serverOptions, err = roleServerOptions(roleCtx, role)
			if err != nil {
				log.Fatalf("Error preparing restarted panel role: %v", err)
			}
			server = web.NewServer(serverOptions...)
			global.SetWebServer(server)
			err = server.StartPanelOnly()
			if err != nil {
				log.Fatalf("Error restarting web server: %v", err)
			}
			log.Println("Web server restarted successfully.")

			sub.SetDistFS(web.EmbeddedDist())
			subServer = sub.NewServer()
			global.SetSubServer(subServer)
			err = subServer.Start()
			if err != nil {
				log.Fatalf("Error restarting sub server: %v", err)
			}
			log.Println("Sub server restarted successfully.")
		case sys.SIGUSR1:
			if role.ManagementOnly() {
				logger.Warning("SIGUSR1 ignored: core restarts belong to node operations")
				continue
			}
			logger.Info("Received USR1 signal, restarting xray-core...")
			err := server.RestartXray()
			if err != nil {
				logger.Error("Failed to restart xray-core:", err)
			}

		default:
			stopRole()
			if stopTunnelHealthMonitor != nil {
				stopTunnelHealthMonitor()
			}

			// --- FIX FOR TELEGRAM BOT CONFLICT (409) on full shutdown ---
			tgbot.StopBot()
			// ------------------------------------------------------------

			_ = server.Stop()
			_ = subServer.Stop()
			log.Println("Shutting down servers.")
			return
		}
	}
}

// resetSetting resets all panel settings to their default values.
func resetSetting() error {
	err := database.InitDB()
	if err != nil {
		fmt.Println("Failed to initialize database:", err)
		return err
	}

	settingService := service.SettingService{}
	err = settingService.ResetSettings()
	if err != nil {
		fmt.Println("Failed to reset settings:", err)
		return err
	} else {
		fmt.Println("Settings successfully reset.")
	}
	return nil
}

// showSetting displays the current panel settings if show is true.
func showSetting(show bool) {
	if show {
		settingService := service.SettingService{}
		port, err := settingService.GetPort()
		if err != nil {
			fmt.Println("get current port failed, error info:", err)
		}

		webBasePath, err := settingService.GetBasePath()
		if err != nil {
			fmt.Println("get webBasePath failed, error info:", err)
		}

		certFile, err := settingService.GetCertFile()
		if err != nil {
			fmt.Println("get cert file failed, error info:", err)
		}
		keyFile, err := settingService.GetKeyFile()
		if err != nil {
			fmt.Println("get key file failed, error info:", err)
		}

		userService := panel.UserService{}
		userModel, err := userService.GetFirstUser()
		if err != nil {
			fmt.Println("get current user info failed, error info:", err)
		}

		if userModel.Username == "" || userModel.Password == "" {
			fmt.Println("current username or password is empty")
		}

		fmt.Println("current panel settings as follows:")
		if certFile == "" || keyFile == "" {
			fmt.Println("Warning: Panel is not secure with SSL")
		} else {
			fmt.Println("Panel is secure with SSL")
		}

		hasDefaultCredential := func() bool {
			return userModel.Username == "admin" && crypto.CheckPasswordHash(userModel.Password, "admin")
		}()

		fmt.Println("hasDefaultCredential:", hasDefaultCredential)
		fmt.Println("port:", port)
		fmt.Println("webBasePath:", webBasePath)
		if webDomain, err := settingService.GetWebDomain(); err == nil {
			fmt.Println("webDomain:", webDomain)
		}
		if sessionMaxAge, err := settingService.GetSessionMaxAge(); err == nil {
			fmt.Println("sessionMaxAge:", sessionMaxAge)
		}
		if trusted, err := settingService.GetTrustedProxyCIDRs(); err == nil {
			fmt.Println("trustedProxyCIDRs:", trusted)
		}
		if subListen, err := settingService.GetSubListen(); err == nil {
			fmt.Println("subListen:", subListen)
		}
		if subPort, err := settingService.GetSubPort(); err == nil {
			fmt.Println("subPort:", subPort)
		}
	}
}

// updateTgbotEnableSts enables or disables the Telegram bot notifications based on the status parameter.
func updateTgbotEnableSts(status bool) {
	settingService := service.SettingService{}
	currentTgSts, err := settingService.GetTgbotEnabled()
	if err != nil {
		fmt.Println(err)
		return
	}
	logger.Infof("current enabletgbot status[%v],need update to status[%v]", currentTgSts, status)
	if currentTgSts != status {
		err := settingService.SetTgbotEnabled(status)
		if err != nil {
			fmt.Println(err)
			return
		} else {
			logger.Infof("SetTgbotEnabled[%v] success", status)
		}
	}
}

// updateTgbotSetting updates Telegram bot settings including token, chat ID, and runtime schedule.
func updateTgbotSetting(tgBotToken string, tgBotChatid string, tgBotRuntime string) {
	err := database.InitDB()
	if err != nil {
		fmt.Println("Error initializing database:", err)
		return
	}

	settingService := service.SettingService{}

	if tgBotToken != "" {
		err := settingService.SetTgBotToken(tgBotToken)
		if err != nil {
			fmt.Printf("Error setting Telegram bot token: %v\n", err)
			return
		}
		logger.Info("Successfully updated Telegram bot token.")
	}

	if tgBotRuntime != "" {
		err := settingService.SetTgbotRuntime(tgBotRuntime)
		if err != nil {
			fmt.Printf("Error setting Telegram bot runtime: %v\n", err)
			return
		}
		logger.Infof("Successfully updated Telegram bot runtime to [%s].", tgBotRuntime)
	}

	if tgBotChatid != "" {
		err := settingService.SetTgBotChatId(tgBotChatid)
		if err != nil {
			fmt.Printf("Error setting Telegram bot chat ID: %v\n", err)
			return
		}
		logger.Info("Successfully updated Telegram bot chat ID.")
	}
}

// updateSetting updates various panel settings including port, credentials, base path, listen IP, and two-factor authentication.
/*
showFirewallPorts prints what this host binds on the panel's behalf.

Read-only, and it talks to the database rather than to a running panel — the
installer calls it with the service stopped. The format is an interface: one
record per line, "<port>/<proto> <role>", stable field order, no header and no
decoration, because a shell script reads it with `while read spec role`.
*/
func showFirewallPorts() error {
	if err := database.InitDB(); err != nil {
		fmt.Fprintln(os.Stderr, "Database initialization failed:", err)
		return err
	}
	ports, err := (&service.InboundService{}).FirewallPorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to list ports:", err)
		return err
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return nil
}

// cliSettingExtras are the start-time settings the panel reads once, which
// used to be repairable only from a browser that could still reach it.
type cliSettingExtras struct {
	sessionMaxAge     int
	trustedProxyCIDRs string
	subListen         string
	subPort           int
}

func applyCliSettingExtras(settingService *service.SettingService, extra cliSettingExtras) {
	if extra.sessionMaxAge > 0 {
		if err := settingService.SetSessionMaxAge(extra.sessionMaxAge); err != nil {
			fmt.Println("Failed to set the session max age:", err)
		} else {
			fmt.Printf("sessionMaxAge set to %d minutes\n", extra.sessionMaxAge)
		}
	}
	if extra.trustedProxyCIDRs != "" {
		value := extra.trustedProxyCIDRs
		if value == "-" {
			value = ""
		}
		if err := settingService.SetTrustedProxyCIDRs(value); err != nil {
			fmt.Println("Failed to set trustedProxyCIDRs:", err)
		} else if value == "" {
			fmt.Println("trustedProxyCIDRs cleared; no peer may set forwarded headers")
		} else {
			fmt.Println("trustedProxyCIDRs set to", value)
		}
	}
	if extra.subListen != "" {
		value := extra.subListen
		if value == "-" {
			value = ""
		}
		if err := settingService.SetSubListen(value); err != nil {
			fmt.Println("Failed to set the subscription listen IP:", err)
		} else {
			fmt.Printf("subListen set to %q\n", value)
		}
	}
	if extra.subPort > 0 {
		if err := settingService.SetSubPort(extra.subPort); err != nil {
			fmt.Println("Failed to set the subscription port:", err)
		} else {
			fmt.Printf("subPort set to %d\n", extra.subPort)
		}
	}
}

func updateSetting(port int, username string, password string, webBasePath string, listenIP string, resetTwoFactor bool, ldapAdminFilter string, webDomain string, extra cliSettingExtras) error {
	err := database.InitDB()
	if err != nil {
		fmt.Println("Database initialization failed:", err)
		return err
	}

	settingService := service.SettingService{}
	userService := panel.UserService{}

	if port > 0 {
		err := settingService.SetPort(port)
		if err != nil {
			fmt.Println("Failed to set port:", err)
		} else {
			fmt.Printf("Port set successfully: %v\n", port)
		}
	}

	if username != "" || password != "" {
		err := userService.UpdateFirstUser(username, password)
		if err != nil {
			fmt.Println("Failed to update username and password:", err)
		} else {
			fmt.Println("Username and password updated successfully")
		}
	}

	if webBasePath != "" {
		err := settingService.SetBasePath(webBasePath)
		if err != nil {
			fmt.Println("Failed to set base URI path:", err)
		} else {
			fmt.Println("Base URI path set successfully")
		}
	}

	// The one way to turn PANEL sign-in through LDAP on. Deliberately not in the
	// settings UI: granting panel administration is a root-shell decision, and
	// keeping it out of AllSetting means a settings save can never clear it.
	/*
		The way back in from a Listen Domain that does not match how you reach the
		panel.

		A wrong value makes every request 403 after a restart — including the
		settings page that would fix it. There was no flag for it, so the only
		recovery was editing the database by hand.
	*/
	if webDomain != "" {
		value := webDomain
		if value == "-" {
			value = ""
		}
		if err := settingService.SetWebDomain(value); err != nil {
			fmt.Println("Failed to set the listen domain:", err)
		} else if value == "" {
			fmt.Println("Listen domain cleared; the panel answers on any host again")
		} else {
			fmt.Println("Listen domain set to", value)
		}
	}

	if ldapAdminFilter != "" {
		value := ldapAdminFilter
		if value == "-" {
			value = ""
		}
		if err := settingService.SetLdapAdminFilter(value); err != nil {
			fmt.Println("Failed to set the LDAP admin filter:", err)
		} else if value == "" {
			fmt.Println("LDAP panel sign-in disabled (admin filter cleared)")
		} else {
			fmt.Println("LDAP admin filter set; panel sign-in through LDAP is now restricted to accounts matching it")
		}
	}

	if resetTwoFactor {
		err := settingService.SetTwoFactorEnable(false)

		if err != nil {
			fmt.Println("Failed to reset two-factor authentication:", err)
		} else {
			_ = settingService.SetTwoFactorToken("")
			fmt.Println("Two-factor authentication reset successfully")
		}
	}

	if listenIP != "" {
		err := settingService.SetListen(listenIP)
		if err != nil {
			fmt.Println("Failed to set listen IP:", err)
		} else {
			fmt.Printf("listen %v set successfully\n", listenIP)
		}
	}
	applyCliSettingExtras(&settingService, extra)

	return nil
}

// updateCert updates the SSL certificate files for the panel.
/*
loadCertificatePair reports whether these two paths are a certificate the panel
could actually serve.

Separate from updateCert so the check can be exercised without a database: the
pair is opened before anything is stored, and standing Postgres up to assert
that would be testing the setting layer instead.

Clearing both is not a pair and loads nothing, which is the reset form.
*/
func loadCertificatePair(publicKey, privateKey string) (*tls.Certificate, error) {
	if (privateKey == "") != (publicKey == "") {
		return nil, errors.New("both public and private key should be entered")
	}
	if publicKey == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(publicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("the certificate and key are not a usable pair: %w", err)
	}
	return &pair, nil
}

/*
updateCert stores the certificate pair, and reports whether it could.

It answers an ERROR now rather than printing one. Every caller is a script: the
installer picks the URL scheme from this, and update.sh does the same. Both used
to read a success they were never given -- the function printed "failed:" and
returned normally, so `p-ui cert` exited 0 on a write that did not happen and
the installer announced https for a panel serving plain HTTP.

The pair is loaded rather than trusted. A path that does not resolve to a usable
certificate and its matching key is the whole failure this reports: an expired
file, a key belonging to a different certificate, a PEM the operator pasted half
of. Storing those is storing a panel that cannot start its own listener.
*/
func updateCert(publicKey string, privateKey string) error {
	if err := database.InitDB(); err != nil {
		return err
	}

	if _, err := loadCertificatePair(publicKey, privateKey); err != nil {
		return err
	}

	settingService := service.SettingService{}
	setters := []struct {
		what string
		set  func(string) error
		file string
	}{
		{"certificate public key", settingService.SetCertFile, publicKey},
		{"certificate private key", settingService.SetKeyFile, privateKey},
		{"certificate for subscription public key", settingService.SetSubCertFile, publicKey},
		{"certificate for subscription private key", settingService.SetSubKeyFile, privateKey},
	}
	for _, setter := range setters {
		if err := setter.set(setter.file); err != nil {
			return fmt.Errorf("set %s failed: %w", setter.what, err)
		}
		fmt.Println("set " + setter.what + " success")
	}
	return nil
}

// GetCertificate displays the current SSL certificate settings if getCert is true.
func GetCertificate(getCert bool) {
	if getCert {
		settingService := service.SettingService{}
		certFile, err := settingService.GetCertFile()
		if err != nil {
			fmt.Println("get cert file failed, error info:", err)
		}
		keyFile, err := settingService.GetKeyFile()
		if err != nil {
			fmt.Println("get key file failed, error info:", err)
		}

		fmt.Println("cert:", certFile)
		fmt.Println("key:", keyFile)
	}
}

// GetListenIP displays the current panel listen IP address if getListen is true.
func GetListenIP(getListen bool) {
	if getListen {

		settingService := service.SettingService{}
		ListenIP, err := settingService.GetListen()
		if err != nil {
			log.Printf("Failed to retrieve listen IP: %v", err)
			return
		}

		fmt.Println("listenIP:", ListenIP)
	}
}

func GetApiToken(getApiToken bool) {
	if !getApiToken {
		return
	}
	err := database.InitDB()
	if err != nil {
		fmt.Println("open database failed, error info:", err)
		return
	}
	apiTokenService := panel.ApiTokenService{}
	tokens, err := apiTokenService.List()
	if err != nil {
		fmt.Println("get apiToken failed, error info:", err)
		return
	}
	if len(tokens) > 0 {
		fmt.Printf("There are %d API token(s) configured. Existing tokens cannot be retrieved in plaintext because only hashes are stored.\n", len(tokens))
		fmt.Println("If you have lost your token, you can manage and generate new tokens through the Panel UI (Settings -> API Tokens).")

		created, err := apiTokenService.RecreateByName("cli-fallback")
		if err != nil {
			fmt.Println("Failed to create a fallback API token:", err)
			return
		}
		fmt.Println("\nThe CLI fallback token has been regenerated (the previous one is now invalid):")
		fmt.Println("apiToken:", created.Token)
		return
	}
	created, err := apiTokenService.Create("install", "", 0)
	if err != nil {
		fmt.Println("create apiToken failed, error info:", err)
		return
	}
	fmt.Println("apiToken:", created.Token)
}

// migrateDb performs database migration operations for the Penhoon UI panel.
func migrateDb() {
	inboundService := service.InboundService{}

	logger.InitLogger(logging.INFO)
	err := database.InitDB()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Start migrating database...")
	inboundService.MigrateDB()
	fmt.Println("Migration done!")
}

// loadServiceEnvFile loads the systemd EnvironmentFile so CLI subcommands like
// "p-ui setting" reach the same database as the panel. godotenv.Load does not
// override variables already in the environment, so it is a no-op for the
// systemd-managed service.
func loadServiceEnvFile() {
	path := config.GetEnvFilePath()
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := godotenv.Load(path); err != nil {
		log.Printf("warning: failed to load env file %s: %v", path, err)
	}
}

// main is the entry point of the Penhoon UI application.
// It parses command-line arguments to run the web server, migrate database, or update settings.
func main() {
	loadServiceEnvFile()
	_ = godotenv.Load()
	role, err := config.RoleFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if handled, err := roleCommand(role, os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := role.ValidatePanel(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if role.ManagementOnly() && len(os.Args) > 1 && (os.Args[1] == "ban" || os.Args[1] == "migrate") {
		fmt.Fprintln(os.Stderr, "legacy host ban and migration commands are unavailable in fleet roles")
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "fleet" {
		if err := database.InitDB(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		err := controlplane.Command(context.Background(), database.GetDB(), os.Args[2:], os.Stdout)
		_ = database.CloseDB()
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 {
		runWebServer()
		return
	}

	var showVersion bool
	flag.BoolVar(&showVersion, "v", false, "show version")

	runCmd := flag.NewFlagSet("run", flag.ExitOnError)

	settingCmd := flag.NewFlagSet("setting", flag.ExitOnError)
	var port int
	var username string
	var password string
	var webBasePath string
	var listenIP string
	var getListen bool
	var webCertFile string
	var webKeyFile string
	var tgbottoken string
	var tgbotchatid string
	var enabletgbot bool
	var tgbotRuntime string
	var reset bool
	var show bool
	var getCert bool
	var getApiToken bool
	var resetTwoFactor bool
	var ldapAdminFilter string
	var webDomain string
	var sessionMaxAge int
	var trustedProxyCIDRs string
	var subListen string
	var subPort int
	var listPorts bool
	settingCmd.BoolVar(&reset, "reset", false, "Reset all settings")
	settingCmd.BoolVar(&show, "show", false, "Display current settings")
	settingCmd.IntVar(&port, "port", 0, "Set panel port number")
	settingCmd.StringVar(&username, "username", "", "Set login username")
	settingCmd.StringVar(&password, "password", "", "Set login password")
	settingCmd.StringVar(&webBasePath, "webBasePath", "", "Set base path for Panel")
	settingCmd.StringVar(&listenIP, "listenIP", "", "set panel listenIP IP")
	settingCmd.BoolVar(&resetTwoFactor, "resetTwoFactor", false, "Reset two-factor authentication settings")
	settingCmd.StringVar(&webDomain, "webDomain", "", "Set the panel Listen Domain (pass \"-\" to clear it and answer on any host)")
	settingCmd.IntVar(&sessionMaxAge, "sessionMaxAge", 0, "Set the panel session length in minutes")
	settingCmd.StringVar(&trustedProxyCIDRs, "trustedProxyCIDRs", "", "Set the proxies allowed to send forwarded headers, comma-separated (pass \"-\" to clear)")
	settingCmd.StringVar(&subListen, "subListen", "", "Set the subscription server listen IP (pass \"-\" to clear)")
	settingCmd.IntVar(&subPort, "subPort", 0, "Set the subscription server port")
	settingCmd.StringVar(&ldapAdminFilter, "ldapAdminFilter", "", "LDAP filter an account must also match to sign in to the PANEL (empty = off; pass \"-\" to clear)")
	settingCmd.BoolVar(&getListen, "getListen", false, "Display current panel listenIP IP")
	settingCmd.BoolVar(&getCert, "getCert", false, "Display current certificate settings")
	settingCmd.BoolVar(&getApiToken, "getApiToken", false, "Display current API token")
	settingCmd.BoolVar(&listPorts, "listPorts", false, "List the ports this host binds, one per line, as \"<port>/<proto> <role>\"")
	settingCmd.StringVar(&webCertFile, "webCert", "", "Set path to public key file for panel")
	settingCmd.StringVar(&webKeyFile, "webCertKey", "", "Set path to private key file for panel")
	settingCmd.StringVar(&tgbottoken, "tgbottoken", "", "Set token for Telegram bot")
	settingCmd.StringVar(&tgbotRuntime, "tgbotRuntime", "", "Set cron time for Telegram bot notifications")
	settingCmd.StringVar(&tgbotchatid, "tgbotchatid", "", "Set chat ID for Telegram bot notifications")
	settingCmd.BoolVar(&enabletgbot, "enabletgbot", false, "Enable notifications via Telegram bot")

	banCmd := flag.NewFlagSet("ban", flag.ExitOnError)
	var bans banOptions
	banCmd.StringVar(&bans.add, "add", "", "Ban an address on every local inbound port")
	banCmd.IntVar(&bans.minutes, "minutes", 0, "How long a -add ban lasts; 0 uses the ban duration setting")
	banCmd.StringVar(&bans.release, "release", "", "Release an address; with -email, only that client's ban of it")
	banCmd.StringVar(&bans.email, "email", "", "The client a -release names")
	banCmd.BoolVar(&bans.releaseAll, "release-all", false, "Release every ban this panel holds")

	oldUsage := flag.Usage
	flag.Usage = func() {
		oldUsage()
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("    role           show process role without opening a database")
		fmt.Println("    preflight      validate role environment without changing the host")
		fmt.Println("    run            run web panel")
		fmt.Println("    migrate        run pending data migrations on the configured database")
		fmt.Println("    fleet          initialize node control and create enrollment packages")
		fmt.Println("    setting        set settings")
		fmt.Println("    ban            list, add or release device-cap bans")
	}

	flag.Parse()
	if showVersion {
		fmt.Println(config.GetPanelVersion())
		return
	}

	switch os.Args[1] {
	case "run":
		err := runCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println(err)
			return
		}
		runWebServer()
	case "migrate":
		migrateDb()
	case "setting":
		err := settingCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println(err)
			return
		}
		if listPorts {
			// Answered before any write path, so a read cannot be a mutation and
			// the installer can ask a stopped panel what to open.
			if err = showFirewallPorts(); err != nil {
				os.Exit(1)
			}
			return
		}
		if reset {
			if err = resetSetting(); err != nil {
				return
			}
		} else {
			if err = updateSetting(port, username, password, webBasePath, listenIP, resetTwoFactor, ldapAdminFilter, webDomain, cliSettingExtras{sessionMaxAge: sessionMaxAge, trustedProxyCIDRs: trustedProxyCIDRs, subListen: subListen, subPort: subPort}); err != nil {
				return
			}
		}
		if webCertFile != "" || webKeyFile != "" {
			// `setting -webCert` reaches the same store as `cert`, and the
			// installer's non-SSL paths use it too, so a refusal must be as loud
			// here as it is there.
			if err = updateCert(webCertFile, webKeyFile); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}
		if show {
			showSetting(show)
		}
		if getListen {
			GetListenIP(getListen)
		}
		if getCert {
			GetCertificate(getCert)
		}
		if getApiToken {
			GetApiToken(getApiToken)
		}
		if (tgbottoken != "") || (tgbotchatid != "") || (tgbotRuntime != "") {
			updateTgbotSetting(tgbottoken, tgbotchatid, tgbotRuntime)
		}
		if enabletgbot {
			updateTgbotEnableSts(enabletgbot)
		}
	case "ban":
		if err := banCmd.Parse(os.Args[2:]); err != nil {
			fmt.Println(err)
			return
		}
		if err := runBanCommand(bans); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	case "cert":
		err := settingCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println(err)
			return
		}
		cert, key := webCertFile, webKeyFile
		if reset {
			cert, key = "", ""
		}
		if err := updateCert(cert, key); err != nil {
			// Non-zero, because the installer and update.sh choose the panel's
			// URL scheme from this. Same shape as showFirewallPorts above.
			fmt.Println(err)
			os.Exit(1)
		}
	default:
		fmt.Println("Invalid subcommands")
		fmt.Println()
		runCmd.Usage()
		fmt.Println()
		settingCmd.Usage()
		fmt.Println()
		banCmd.Usage()
	}
}
