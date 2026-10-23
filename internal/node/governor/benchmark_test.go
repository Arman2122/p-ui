package governor

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func BenchmarkDurableRuntimeAdmission(b *testing.B) {
	store, err := Open(filepath.Join(b.TempDir(), "governor.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	accounts := make([]*limits.Account, 64)
	for i := range accounts {
		grant := grantForTest(0)
		grant.Policy.QuotaUnlimited = true
		if err := store.Apply(grant); err != nil {
			b.Fatal(err)
		}
		accounts[i], _ = store.Lookup(grant.ClientID)
	}
	var worker atomic.Uint64
	b.SetBytes(16 << 10)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		account := accounts[(worker.Add(1)-1)%uint64(len(accounts))]
		for pb.Next() {
			if err := account.Take(context.Background(), limits.Upload, 16<<10); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
