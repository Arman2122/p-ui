package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

func Command(ctx context.Context, db *gorm.DB, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: p-ui fleet init|enroll --config FILE")
	}
	flags := flag.NewFlagSet("fleet "+args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	configPath := flags.String("config", "/etc/penhoon/control.json", "master control configuration")
	name := flags.String("name", "", "node name for enrollment")
	destination := flags.String("out", "", "private enrollment package output path")
	authorityPath := flags.String("authority", "/var/lib/penhoon/control/authority.json", "private master authority file")
	controlListen := flags.String("control-listen", ":7443", "control listener address")
	enrollmentListen := flags.String("enrollment-listen", ":7444", "HTTPS enrollment listener address")
	controlAddress := flags.String("control-address", "", "control address advertised to nodes")
	enrollmentURL := flags.String("enrollment-url", "", "HTTPS enrollment URL advertised to nodes, ending in /enroll")
	serverName := flags.String("server-name", "", "TLS server name override for the control address")
	requestStdin := flags.Bool("request-stdin", false, "read a durably prepared enrollment request from stdin and write its private package to stdout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*configPath) {
		return ErrInvalid
	}
	store, err := NewStore(db)
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		config := HostConfig{Version: 1, AuthorityFile: *authorityPath, ControlListen: *controlListen, EnrollmentListen: *enrollmentListen, ControlAddress: *controlAddress, EnrollmentURL: *enrollmentURL, ServerName: *serverName}
		names, err := config.names()
		if err != nil {
			return err
		}
		if _, err := os.Lstat(*configPath); err == nil {
			return fmt.Errorf("configuration already exists: %s", *configPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := CreateAuthorityFile(*authorityPath, names); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		host, err := NewHost(ctx, store, config)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*configPath), 0o755); err != nil {
			return err
		}
		if err := writeNewJSON(*configPath, config, 0o644); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Control plane initialized: %s\n", host.ControlPlaneID())
		return err
	case "enroll":
		if *requestStdin {
			if *name != "" || *destination != "" {
				return ErrInvalid
			}
			request, err := readEnrollmentRequest(os.Stdin)
			if err != nil {
				return err
			}
			config, err := ReadHostConfig(*configPath)
			if err != nil {
				return err
			}
			host, err := NewHost(ctx, store, config)
			if err != nil {
				return err
			}
			pack, err := host.enrollRequest(ctx, request)
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(pack)
		}
		if *destination == "" || !filepath.IsAbs(*destination) || *name == "" {
			return errors.New("enrollment requires --name and an absolute --out path")
		}
		config, err := ReadHostConfig(*configPath)
		if err != nil {
			return err
		}
		host, err := NewHost(ctx, store, config)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(*destination); err == nil {
			return os.ErrExist
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pack, err := host.Enroll(ctx, *name)
		if err != nil {
			return err
		}
		if err := writeNewJSON(*destination, pack, 0o600); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Private enrollment package written. It expires in ten minutes.")
		return err
	default:
		return errors.New("unknown fleet command")
	}
}

func writeNewJSON(path string, value any, mode os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".fleet-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := privatefile.RenameNew(temporary, path); err != nil {
		return err
	}
	return privatefile.SyncDirectory(dir)
}
