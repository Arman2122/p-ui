package enforcement

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/xtls/xray-core/common/buf"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type terminalFragments struct{ data buf.MultiBuffer }

func (r *terminalFragments) ReadMultiBuffer() (buf.MultiBuffer, error) {
	data := r.data
	r.data = nil
	return data, io.EOF
}

func TestFragmentedStreamAccountsOnlyForwardedBytes(t *testing.T) {
	for _, path := range []string{"reader", "writer"} {
		for _, ceiling := range []string{"unlimited", "exact quota"} {
			t.Run(path+"/"+ceiling, func(t *testing.T) {
				payload := bytes.Repeat([]byte("v"), 22+int(buf.Size))
				credit := uint64(0)
				if ceiling == "exact quota" {
					credit = uint64(len(payload))
				}
				account, err := limits.New(limits.Policy{
					Term: "fragmented-stream", Revision: 1, Enabled: true, QuotaUnlimited: ceiling == "unlimited", CreditCeiling: credit,
					Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
				})
				if err != nil {
					t.Fatal(err)
				}
				// Vision can expose a short TLS suffix before a full raw buffer.
				data := buf.MultiBuffer{buf.FromBytes(payload[:22]), buf.FromBytes(payload[22:])}
				if ceiling == "exact quota" {
					data = append(data, buf.FromBytes([]byte("must not forward")))
				}
				var output bytes.Buffer
				if path == "writer" {
					w := writer{ctx: t.Context(), base: buf.NewWriter(&output), account: account, dir: limits.Upload}
					err = w.WriteMultiBuffer(data)
				} else {
					r := newReader(t.Context(), &terminalFragments{data: data}, account, limits.Download, false)
					defer r.Close()
					err = buf.Copy(r, buf.NewWriter(&output))
				}
				if ceiling == "exact quota" && !errors.Is(err, limits.ErrQuota) || ceiling == "unlimited" && err != nil {
					t.Fatalf("fragmented stream result: %v", err)
				}
				if !bytes.Equal(output.Bytes(), payload) {
					t.Fatalf("quota forwarded %d bytes, want all %d authorized bytes", output.Len(), len(payload))
				}
				snapshot := account.Snapshot()
				admitted, measured := snapshot.AdmittedUpload+snapshot.AdmittedDownload, snapshot.MeasuredUpload+snapshot.MeasuredDownload
				if admitted != uint64(len(payload)) || measured != uint64(len(payload)) {
					t.Fatalf("fragmented accounting admitted=%d measured=%d, forwarded=%d", admitted, measured, output.Len())
				}
			})
		}
	}
}
