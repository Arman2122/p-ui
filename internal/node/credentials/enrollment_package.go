package credentials

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

func ReadEnrollmentPackage(path string) (EnrollmentPackage, error) {
	var pack EnrollmentPackage
	file, err := privatefile.Open(path, os.O_RDONLY, 0)
	if err != nil {
		return pack, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (128<<10)+1))
	if err != nil {
		return pack, err
	}
	if len(data) > 128<<10 {
		return pack, ErrConfiguration
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&pack) != nil || decoder.Decode(new(any)) != io.EOF || len(pack.Ticket) != 43 {
		return pack, ErrConfiguration
	}
	if pack.ExpiresUnixMilli <= time.Now().UnixMilli() {
		return pack, ErrConfiguration
	}
	if _, err := pack.Configuration.validate(); err != nil {
		return pack, err
	}
	return pack, nil
}
