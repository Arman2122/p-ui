package service

import (
	"encoding/json"
	"testing"

	json_util "github.com/Arman2122/p-ui/internal/util/json_util"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
An operator's own outbound tag must survive a subscription that wants it.

mergeSubscriptionOutbounds concatenated three lists — subscription outbounds
first (they are Prepended), then the template's, then generated ones — with no
check that a tag appeared once. Two things then went wrong, and which one you
got depended on the core:

  - xray's AddHandler hard-refuses a duplicate tag, so the collision costs an
    outbound entirely; and
  - because subscriptions come FIRST, a first-wins resolution hands the
    operator's tag to a third-party server. Every routing rule naming that tag
    then sends its traffic somewhere nobody chose.

So the dedup is by ownership rather than position: the template's tags are the
operator's and are kept unconditionally. Dropped rather than suffixed — a
suffixed tag breaks nothing visibly and routes nowhere, because the rules still
name the original. F078.
*/

func taggedOutbound(tag, protocol string) any {
	return map[string]any{"tag": tag, "protocol": protocol}
}

func mergedTags(t *testing.T, template []any, prepend, appendList []any) []string {
	t.Helper()
	raw, err := json.Marshal(template)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	cfg := &xray.Config{OutboundConfigs: json_util.RawMessage(raw)}
	mergeSubscriptionOutbounds(cfg, prepend, appendList)

	var merged []any
	if err := json.Unmarshal(cfg.OutboundConfigs, &merged); err != nil {
		t.Fatalf("read merged outbounds: %v\n%s", err, cfg.OutboundConfigs)
	}
	tags := make([]string, 0, len(merged))
	for _, entry := range merged {
		object, _ := entry.(map[string]any)
		tag, _ := object["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

func countTag(tags []string, want string) int {
	n := 0
	for _, tag := range tags {
		if tag == want {
			n++
		}
	}
	return n
}

func TestASubscriptionCannotTakeAnOperatorsOutboundTag(t *testing.T) {
	template := []any{taggedOutbound("direct", "freedom"), taggedOutbound("my-vps", "vless")}
	prepend := []any{taggedOutbound("my-vps", "vmess"), taggedOutbound("sub1-tokyo", "vless")}

	tags := mergedTags(t, template, prepend, nil)

	if n := countTag(tags, "my-vps"); n != 1 {
		t.Fatalf("tag my-vps appears %d times in %v; xray refuses a duplicate tag outright", n, tags)
	}
	// And the survivor must be the operator's, not the subscription's.
	if tags[0] == "my-vps" {
		t.Fatalf("the subscription's outbound won the tag: it is prepended, so first-wins hands an operator's tag to a third-party server. Got %v", tags)
	}
	if countTag(tags, "sub1-tokyo") != 1 {
		t.Fatalf("the uncontested subscription outbound was lost: %v", tags)
	}
	if countTag(tags, "direct") != 1 {
		t.Fatalf("a template outbound was lost: %v", tags)
	}
}

// Two subscriptions sharing a tag: one survives, deterministically.
func TestTwoSubscriptionsCannotBothHoldOneTag(t *testing.T) {
	template := []any{taggedOutbound("direct", "freedom")}
	prepend := []any{taggedOutbound("shared", "vless"), taggedOutbound("shared", "vmess")}

	tags := mergedTags(t, template, prepend, nil)

	if n := countTag(tags, "shared"); n != 1 {
		t.Fatalf("tag shared appears %d times in %v", n, tags)
	}
}

// The generated list is appended AFTER the template, so it loses a collision
// too — the ownership rule does not depend on which side of the template it is.
func TestAGeneratedOutboundCannotTakeATemplateTag(t *testing.T) {
	template := []any{taggedOutbound("direct", "freedom")}
	appendList := []any{taggedOutbound("direct", "blackhole"), taggedOutbound("peg1", "freedom")}

	tags := mergedTags(t, template, nil, appendList)

	if n := countTag(tags, "direct"); n != 1 {
		t.Fatalf("tag direct appears %d times in %v", n, tags)
	}
	if countTag(tags, "peg1") != 1 {
		t.Fatalf("the uncontested generated outbound was lost: %v", tags)
	}
}

// An entry with no tag is not a collision and must pass through: xray gives an
// untagged outbound a generated name, and dropping them would silently thin the
// config.
func TestUntaggedOutboundsAreNotTreatedAsColliding(t *testing.T) {
	template := []any{taggedOutbound("direct", "freedom")}
	prepend := []any{map[string]any{"protocol": "freedom"}, map[string]any{"protocol": "blackhole"}}

	tags := mergedTags(t, template, prepend, nil)

	if len(tags) != 3 {
		t.Fatalf("want all three outbounds, got %d: %v", len(tags), tags)
	}
}
