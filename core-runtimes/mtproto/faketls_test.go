package mtproto

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
)

func fakeTLSClient(t *testing.T, raw essentials.Conn, secret mtglib.Secret) essentials.Conn {
	t.Helper()
	session := make([]byte, 32)
	if _, err := rand.Read(session); err != nil {
		t.Fatal(err)
	}
	sni := secret.Host
	extensions := []byte{0, 0}
	extensions = binary.BigEndian.AppendUint16(extensions, uint16(5+len(sni)))
	extensions = binary.BigEndian.AppendUint16(extensions, uint16(3+len(sni)))
	extensions = append(extensions, 0)
	extensions = binary.BigEndian.AppendUint16(extensions, uint16(len(sni)))
	extensions = append(extensions, []byte(sni)...)
	body := append([]byte{3, 3}, make([]byte, 32)...)
	body = append(body, byte(len(session)))
	body = append(body, session...)
	body = append(body, 0, 2, 0x13, 1, 1, 0)
	body = binary.BigEndian.AppendUint16(body, uint16(len(extensions)))
	body = append(body, extensions...)
	hello := []byte{0x16, 3, 1}
	hello = binary.BigEndian.AppendUint16(hello, uint16(len(body)+4))
	hello = append(hello, 1, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	hello = append(hello, body...)
	digest := hmac.New(sha256.New, secret.Key[:])
	digest.Write(hello)
	copy(hello[11:43], digest.Sum(nil))
	stamp := binary.LittleEndian.AppendUint32(nil, uint32(time.Now().Unix()))
	for i := range stamp {
		hello[39+i] ^= stamp[i]
	}
	if _, err := raw.Write(hello); err != nil {
		t.Fatal(err)
	}
	var flight []byte
	for _, kind := range []byte{0x16, 0x14, 0x17} {
		record, err := readRecord(raw, kind)
		if err != nil {
			t.Fatal(err)
		}
		flight = append(flight, record...)
	}
	if len(flight) < 43 {
		t.Fatal("short FakeTLS server flight")
	}
	serverRandom := bytes.Clone(flight[11:43])
	clear(flight[11:43])
	digest.Reset()
	digest.Write(hello[11:43])
	digest.Write(flight)
	if !hmac.Equal(serverRandom, digest.Sum(nil)) {
		t.Fatal("FakeTLS server authentication failed")
	}
	return &recordConn{Conn: raw}
}

func readRecord(reader io.Reader, kind byte) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(header[3:]))
	if header[0] != kind || header[1] != 3 || header[2] != 3 || n == 0 || n > 16379 {
		return nil, fmt.Errorf("invalid FakeTLS record")
	}
	record := append(header, make([]byte, n)...)
	_, err := io.ReadFull(reader, record[5:])
	return record, err
}

type recordConn struct {
	essentials.Conn
	pending []byte
}

func (c *recordConn) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		record, err := readRecord(c.Conn, 0x17)
		if err != nil {
			return 0, err
		}
		c.pending = record[5:]
	}
	n := copy(data, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *recordConn) Write(data []byte) (int, error) {
	written := 0
	for len(data) != 0 {
		n := min(len(data), 16379)
		record := binary.BigEndian.AppendUint16([]byte{0x17, 3, 3}, uint16(n))
		record = append(record, data[:n]...)
		count, err := c.Conn.Write(record)
		if err != nil {
			return written, err
		}
		if count != len(record) {
			return written, io.ErrShortWrite
		}
		written += n
		data = data[n:]
	}
	return written, nil
}
