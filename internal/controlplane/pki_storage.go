package controlplane

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

var authorityFileMu sync.Mutex

func writeAuthority(path string, material AuthorityMaterial, replace bool) error {
	dir := filepath.Dir(path)
	if !filepath.IsAbs(path) {
		return ErrInvalid
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS == "linux" && info.Mode().Perm()&0o077 != 0 {
		return ErrAuthority
	}
	file, err := os.CreateTemp(dir, ".authority-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	encoded, err := json.Marshal(material)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if replace {
		err = os.Rename(temporary, path)
	} else {
		err = privatefile.RenameNew(temporary, path)
	}
	if err != nil {
		return err
	}
	return privatefile.SyncDirectory(dir)
}

func RefreshAuthorityFile(path string, names []string, now time.Time) (AuthorityMaterial, error) {
	authorityFileMu.Lock()
	defer authorityFileMu.Unlock()
	material, err := LoadAuthorityFile(path)
	if err != nil {
		return material, err
	}
	issuer, err := material.issuer(now)
	if err != nil {
		return material, err
	}
	certificate, err := tls.X509KeyPair(material.ServerCertificate, material.ServerKey)
	if err != nil || len(certificate.Certificate) != 1 {
		return material, ErrAuthority
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || leaf.IsCA || leaf.CheckSignatureFrom(issuer.Certificate) != nil || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return material, ErrAuthority
	}
	if _, _, err := material.TLS(names, now); err == nil {
		if leaf.NotAfter.After(now.Add(30*24*time.Hour)) || leaf.NotAfter.Equal(issuer.Certificate.NotAfter) {
			return material, nil
		}
	}
	allNames := slices.Clone(names)
	allNames = append(allNames, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		allNames = append(allNames, ip.String())
	}
	slices.Sort(allNames)
	allNames = slices.Compact(allNames)
	material.ServerCertificate, material.ServerKey, err = makeServerCertificate(issuer.Certificate, issuer.Signer, allNames, now)
	if err != nil {
		return material, err
	}
	if _, _, err := material.TLS(names, now); err != nil {
		return material, err
	}
	if err := writeAuthority(path, material, true); err != nil {
		return material, err
	}
	return material, nil
}
