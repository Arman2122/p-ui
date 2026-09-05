package singbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
)

// sing-box renames its stats service to Xray's at init (experimental/v2rayapi/
// stats.go) so Xray clients work against it; the path and structs are Xray's.
const queryStatsMethod = "/v2ray.core.app.stats.command.StatsService/QueryStats"

const subscribeConnectionsMethod = "/daemon.StartedService/SubscribeConnections"

func dial(port int) (*grpc.ClientConn, error) {
	return grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// queryUserStats reads every user counter the daemon holds: "user>>>NAME>>>
// traffic>>>uplink" and its downlink twin, cumulative since the last (re)load.
func queryUserStats(ctx context.Context, port int) (map[string]int64, error) {
	conn, err := dial(port)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp := &statsService.QueryStatsResponse{}
	if err := conn.Invoke(ctx, queryStatsMethod, &statsService.QueryStatsRequest{Pattern: "user>>>"}, resp); err != nil {
		return nil, fmt.Errorf("singbox: query stats on %d: %w", port, err)
	}
	out := make(map[string]int64, len(resp.Stat))
	for _, s := range resp.Stat {
		out[s.Name] = s.Value
	}
	return out, nil
}

// splitStatName turns "user>>>a@x>>>traffic>>>uplink" into (a@x, uplink).
func splitStatName(name string) (email, dir string, ok bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// liveConnection is the slice of the daemon's Connection message the panel
// reads: who, from where, and when it opened.
type liveConnection struct {
	id        string
	user      string
	source    netip.Addr
	createdAt int64
	// bytes is uplink plus downlink so far; a snapshot where it moved is activity.
	bytes int64
}

/*
listConnections opens the daemon's connection stream and takes its first
message, which carries every live connection with reset set; the stream is
then closed. A snapshot on demand, so the panel keeps no stream per daemon.
*/
func listConnections(ctx context.Context, port int, secret string) ([]liveConnection, error) {
	conn, err := dial(port)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
	desc := &grpc.StreamDesc{StreamName: "SubscribeConnections", ServerStreams: true}
	stream, err := conn.NewStream(ctx, desc, subscribeConnectionsMethod, grpc.ForceCodec(rawCodec{}))
	if err != nil {
		return nil, fmt.Errorf("singbox: subscribe connections on %d: %w", port, err)
	}
	// interval is a Go duration in nanoseconds; an hour keeps the daemon from
	// pushing a second frame before the stream is closed.
	req := protowire.AppendTag(nil, 1, protowire.VarintType)
	req = protowire.AppendVarint(req, uint64(time.Hour))
	if err := stream.SendMsg(rawMessage(req)); err != nil {
		return nil, err
	}
	_ = stream.CloseSend()
	var frame rawMessage
	if err := stream.RecvMsg(&frame); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("singbox: read connections on %d: %w", port, err)
	}
	return decodeConnectionEvents(frame)
}

// decodeConnectionEvents walks ConnectionEvents{events=1}.ConnectionEvent{
// connection=3}.Connection{id=1,source=6,user=10,createdAt=12,closedAt=13,totals=16,17}.
func decodeConnectionEvents(b []byte) ([]liveConnection, error) {
	var out []liveConnection
	err := walkFields(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		if num != 1 || typ != protowire.BytesType {
			return nil
		}
		return walkFields(v, func(num protowire.Number, typ protowire.Type, v []byte) error {
			if num != 3 || typ != protowire.BytesType {
				return nil
			}
			c, closed, err := decodeConnection(v)
			if err == nil && !closed && c.user != "" {
				out = append(out, c)
			}
			return err
		})
	})
	return out, err
}

func decodeConnection(b []byte) (c liveConnection, closed bool, err error) {
	err = walkFields(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			c.id = string(v)
		case num == 6 && typ == protowire.BytesType:
			c.source = parseSource(string(v))
		case num == 10 && typ == protowire.BytesType:
			c.user = string(v)
		case num == 12 && typ == protowire.VarintType:
			n, _ := protowire.ConsumeVarint(v)
			c.createdAt = int64(n)
		case num == 13 && typ == protowire.VarintType:
			n, _ := protowire.ConsumeVarint(v)
			closed = n != 0
		case (num == 16 || num == 17) && typ == protowire.VarintType:
			n, _ := protowire.ConsumeVarint(v)
			c.bytes += int64(n)
		}
		return nil
	})
	return c, closed, err
}

// parseSource reads "host:port" or a bare address; an unparseable one is the
// zero Addr, which the session view shows as unknown rather than wrong.
func parseSource(s string) netip.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr()
	}
	addr, _ := netip.ParseAddr(strings.Trim(s, "[]"))
	return addr
}

// walkFields visits every top-level field of one message; a varint's value is
// handed over re-encoded so the visitor decodes it with one helper.
func walkFields(b []byte, visit func(num protowire.Number, typ protowire.Type, v []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		var v []byte
		switch typ {
		case protowire.VarintType:
			x, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			v, b = protowire.AppendVarint(nil, x), b[m:]
		case protowire.BytesType:
			s, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			v, b = s, b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			b = b[m:]
			continue
		}
		if err := visit(num, typ, v); err != nil {
			return err
		}
	}
	return nil
}

// rawMessage and rawCodec move already-encoded protobuf bytes through grpc, so
// the daemon API needs no generated types for the two messages the panel reads.
type rawMessage []byte

type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) ([]byte, error) {
	m, ok := v.(rawMessage)
	if !ok {
		return nil, fmt.Errorf("singbox: rawCodec cannot marshal %T", v)
	}
	return m, nil
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	m, ok := v.(*rawMessage)
	if !ok {
		return fmt.Errorf("singbox: rawCodec cannot unmarshal into %T", v)
	}
	*m = append((*m)[:0], data...)
	return nil
}

const closeConnectionMethod = "/daemon.StartedService/CloseConnection"

// closeConnection ends one live connection by the id the connection stream gave
// it; CloseConnectionRequest{id=1}, answered with Empty.
func closeConnection(ctx context.Context, port int, secret, id string) error {
	conn, err := dial(port)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
	req := protowire.AppendTag(nil, 1, protowire.BytesType)
	req = protowire.AppendString(req, id)
	var frame rawMessage
	if err := conn.Invoke(ctx, closeConnectionMethod, rawMessage(req), &frame, grpc.ForceCodec(rawCodec{})); err != nil {
		return fmt.Errorf("singbox: close connection %s on %d: %w", id, port, err)
	}
	return nil
}

const getStartedAtMethod = "/daemon.StartedService/GetStartedAt"

// startedAt is when the daemon's current instance came up; it changes on a
// reload, which is how the panel knows a SIGHUP has landed.
func startedAt(ctx context.Context, port int, secret string) (int64, error) {
	conn, err := dial(port)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
	var frame rawMessage
	if err := conn.Invoke(ctx, getStartedAtMethod, rawMessage(nil), &frame, grpc.ForceCodec(rawCodec{})); err != nil {
		return 0, fmt.Errorf("singbox: started-at on %d: %w", port, err)
	}
	var at int64
	err = walkFields(frame, func(num protowire.Number, typ protowire.Type, v []byte) error {
		if num == 1 && typ == protowire.VarintType {
			n, _ := protowire.ConsumeVarint(v)
			at = int64(n)
		}
		return nil
	})
	return at, err
}
