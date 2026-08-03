package database

import "fmt"

// TrafficMax caps every traffic counter safely below math.MaxInt64 (~9.22e18)
// so that one more delta can never overflow int64 and break every reader of the
// table (#5762).
const TrafficMax = int64(9_000_000_000_000_000_000)

// ClampedAddExpr builds the saturating `col + ?` used by every traffic writer,
// so a runaway delta stops at TrafficMax instead of wrapping.
func ClampedAddExpr(col string) string {
	return fmt.Sprintf("LEAST(%s + ?, %d)", col, TrafficMax)
}

// JSONClientsFromInbound expands each inbound's settings.clients array into one
// row per client, aliased as client(value).
func JSONClientsFromInbound() string {
	return "FROM inbounds, jsonb_array_elements(inbounds.settings::jsonb -> 'clients') AS client(value)"
}

// JSONFieldText reads a JSON object key as text.
func JSONFieldText(expr, key string) string {
	return fmt.Sprintf("(%s ->> '%s')", expr, key)
}

// GreatestExpr returns the larger of two integer expressions.
func GreatestExpr(a, b string) string {
	return fmt.Sprintf("GREATEST(%s::bigint, %s::bigint)", a, b)
}

// ClientTrafficEnableMergeExpr keeps a client enabled only when the incoming
// snapshot says so, matching the boolean typing Postgres requires.
func ClientTrafficEnableMergeExpr() string {
	return "CASE WHEN ?::boolean THEN enable::boolean ELSE false END"
}
