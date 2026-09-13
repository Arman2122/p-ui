package singbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// BinaryName is the bundled binary's name, by GOARCH like mtg's: release.yml
// emits it under this name so no installer rename is needed.
func BinaryName() string { return "sing-box-linux-" + runtime.GOARCH }

func (c *Core) binaryPath() string {
	if c.deps.Binary != "" {
		return c.deps.Binary
	}
	return filepath.Join(config.GetBinFolderPath(), BinaryName())
}

// stateDir is where configs and certificate copies live: the installer's
// /var/lib/p-ui when it exists, else beside the binaries on a dev box.
func (c *Core) stateDir() string {
	if c.deps.StateDir != "" {
		return c.deps.StateDir
	}
	if info, err := os.Stat("/var/lib/p-ui"); err == nil && info.IsDir() {
		return "/var/lib/p-ui/singbox"
	}
	return filepath.Join(config.GetBinFolderPath(), "singbox")
}

func (c *Core) instanceDir(id int) string {
	return filepath.Join(c.stateDir(), strconv.Itoa(id))
}

// ensureDir makes the instance directory and the state root above it, both
// traversable by the daemon group: MkdirAll alone leaves the root root:root.
func (c *Core) ensureDir(dir string) error {
	for _, path := range []string{c.stateDir(), dir} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o750); err != nil {
			return err
		}
		if err := c.chgrp(path); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) chgrp(path string) error {
	if c.deps.Group < 0 {
		return nil
	}
	if err := os.Chown(path, -1, c.deps.Group); err != nil && !os.IsPermission(err) {
		return fmt.Errorf("singbox: chgrp %s: %w", path, err)
	}
	return nil
}

// writeFile lands content atomically at 0640 for the daemon group: a config
// carries every client's password and the API secret.
func (c *Core) writeFile(path string, body []byte) error {
	staged, err := c.stageFile(path, body)
	if err != nil {
		return err
	}
	return os.Rename(staged, path)
}

// stageFile writes body beside path, for a check to read before it is committed.
func (c *Core) stageFile(path string, body []byte) (string, error) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return "", err
	}
	if err := c.chgrp(tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

/*
certPair keeps a copy of the inbound's certificate and key the daemon user
can read; the operator's originals are root 0600 as certbot leaves them. The
copy is refreshed whenever the original changes, and sing-box watches its own
paths, so a renewed certificate reaches clients without a reload.
*/
func (c *Core) certPair(dir string, settings Settings) (certFile, keyFile string, err error) {
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for _, pair := range [][2]string{{settings.CertFile, certFile}, {settings.KeyFile, keyFile}} {
		if err := c.syncCopy(pair[0], pair[1]); err != nil {
			return "", "", err
		}
	}
	return certFile, keyFile, nil
}

func (c *Core) syncCopy(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("singbox: %s: %w", src, err)
	}
	if dstInfo, err := os.Stat(dst); err == nil && dstInfo.Size() == srcInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("singbox: %s: %w", src, err)
	}
	defer in.Close()
	body, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return c.writeFile(dst, body)
}

func newSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomToken(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	out := make([]byte, n)
	for i, b := range raw {
		out[i] = tokenAlphabet[int(b)%len(tokenAlphabet)]
	}
	return string(out)
}

// spec is the unit for one inbound. The daemon user and the port-binding
// capability go in only under systemd; the exec backend refuses a user.
func (c *Core) spec(inst core.Instance, dir, cfgPath string) supervisor.Spec {
	s := supervisor.Spec{
		Name:               supervisor.UnitName(string(ID), inst.ID),
		Description:        fmt.Sprintf("Penhoon UI %s inbound %d", inst.Kind, inst.ID),
		Exec:               []string{c.binaryPath(), "run", "-c", cfgPath, "-D", dir, "--disable-color"},
		WorkingDir:         dir,
		Restart:            supervisor.RestartOnFailure,
		RestartAfter:       2 * time.Second,
		StartLimitInterval: time.Minute,
		StartLimitBurst:    10,
		StopTimeout:        10 * time.Second,
		KillSignal:         syscall.SIGTERM,
		Output:             logWriter{tag: inst.Tag},
	}
	if c.backend != nil && c.backend.Name() == string(supervisor.ModeSystemd) && c.deps.User != "" {
		s.User = c.deps.User
		s.Capabilities = []string{"CAP_NET_BIND_SERVICE"}
	}
	return s
}

// logWriter forwards a child's stderr into the panel log under the exec
// backend; systemd keeps the journal itself.
type logWriter struct{ tag string }

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			logger.Infof("sing-box[%s]: %s", w.tag, line)
		}
	}
	return len(p), nil
}
