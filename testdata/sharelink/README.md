# Share-link golden corpus

One inbound row, one set of link parameters every renderer must produce from it.

Share links are built more than once in this repo: the Go subscription renderer
in `internal/sub/`, the frontend builder in `frontend/src/lib/xray/`, and — for
the encoding half only — a third copy in `docs/lib/xray/`. Nothing enforced
agreement between them, and the failure shape is always the same sentence with
different nouns: renderer A emits a field renderer B omits, so the panel shows
one tunnel and the subscription hands out another.

Each fixture here is read by every harness. Adding a renderer means adding a
harness; adding a field means adding it to `expected.params` and then to
whichever renderer does not yet emit it.

## Harnesses

| Renderer | Harness |
| --- | --- |
| `internal/sub` | `internal/sub/golden_corpus_test.go` |
| `frontend/src/lib/xray` | `frontend/src/test/sharelink-corpus.test.ts` |

## Fixture shape

```jsonc
{
  "name": "vless-ws-tls",          // also the file name
  "why": "what this fixture is for",
  "email": "golden",               // which client of the inbound to render
  "inbound": { ... },              // the database row, settings as JSON strings
  "expected": {
    "scheme": "vless",
    "userinfo": "…",               // "" when the scheme carries none
    "host": "203.0.113.10",
    "port": 443,
    "params": { "type": "ws", … }  // the REQUIRED subset, not the whole query
  },
  "presentOnly": ["spx"],          // params that must exist, value unchecked
  "absentParams": ["flow"]         // params that must NOT be emitted
}
```

`expected.params` is a required subset, deliberately: a renderer may emit more,
and the corpus grows by declaring more rather than by freezing whatever one
implementation happens to do today. A value that is derived but deterministic
(`spx`) belongs in `params` with its computed value, not in `presentOnly` —
that is where a divergence in the derivation itself gets caught.

`absentParams` is how a fixture pins a field the renderer must stop emitting.
It is a separate list rather than a declared empty value because Go reads an
absent query param as `""` and the browser reads it as `null`, so no single
declared value passes on both harnesses.

The `#fragment` is **not** compared. Go composes it from the panel's remark
template while the frontend takes it as an input, so the two are answering
different questions. Every finding this corpus exists for lived in the tunnel
parameters, not the name.
