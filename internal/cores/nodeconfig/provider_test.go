package nodeconfig

import (
	"errors"
	"testing"
)

func TestProviderValidationRefusesCaseInsensitiveFileReferences(t *testing.T) {
	for _, field := range []string{"certificateFile", "CertificateFile", "CERTIFICATEFILE"} {
		raw := `[{"protocol":"trojan","tag":"fixture","settings":{"servers":[{"address":"edge.example","port":443,"password":"fixture"}]},"streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"` + field + `":"provider-file-fixture"}]}}}]`
		if err := ValidateXrayProviderOutbounds([]byte(raw)); !errors.Is(err, ErrDependency) {
			t.Fatalf("provider file reference %s reached core build: %v", field, err)
		}
	}
}
