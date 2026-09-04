package core

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// routingFake is the smallest core that satisfies the contract, so each test
// below adds exactly one routing declaration and nothing else varies.
type routingFake struct{ kinds []Kind }

func (routingFake) Describe() Descriptor            { return Descriptor{ID: "fake", TitleKey: "cores.fake.title"} }
func (f routingFake) Kinds() []Kind                 { return f.kinds }
func (routingFake) Validate(Instance) error         { return nil }
func (routingFake) Preflight(context.Context) error { return nil }

type ingressFake struct {
	routingFake
	selector IngressSelector
}

func (f ingressFake) IngressSelector(Kind) IngressSelector { return f.selector }
func (ingressFake) IngressHandle(context.Context, Instance) (IngressHandle, error) {
	return IngressHandle{}, nil
}

// Every routable ingress says what a rule can match its traffic on, and the
// addresses are what any of them can answer. F060.
func (ingressFake) IngressCriteria(Kind) []Criterion { return CriteriaByAddress() }

type egressFake struct {
	routingFake
	exits []Kind
}

func (f egressFake) ExitKinds() []Kind                { return f.exits }
func (egressFake) ExitHandleKind(Kind) ExitHandleKind { return ExitDevice }
func (egressFake) ExitHandle(context.Context, Exit) (ExitHandle, error) {
	return ExitHandle{}, nil
}

// silentIngressFake routes a kind and then names nothing a rule could match it
// on, which is a core that cannot be routed saying it can.
type silentIngressFake struct{ ingressFake }

func (silentIngressFake) IngressCriteria(Kind) []Criterion { return nil }

/*
A nil declaration is a conformance failure, not a default: a core that implements
the interface and then answers "nothing" everywhere costs a caller a type
assertion and a call to learn what not implementing it would have said for free.
*/
func TestDeclaredMatchesImplementedCatchesEmptyRoutingDeclaration(t *testing.T) {
	base := routingFake{kinds: []Kind{"fk"}}

	for _, tc := range []struct {
		name string
		core Core
		want string
	}{
		{
			"an ingress that routes no kind declares nothing",
			ingressFake{routingFake: base, selector: IngressNone},
			"RoutableIngress: implemented, but every kind answers IngressNone",
		},
		{
			"an ingress that names no criterion declares nothing",
			silentIngressFake{ingressFake{routingFake: base, selector: IngressInternal}},
			"RoutableIngress: implemented, but every kind names no criterion",
		},
		{
			"an egress that offers no exit kind declares nothing",
			egressFake{routingFake: base},
			"RoutableEgress: implemented, but ExitKinds is empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := Bind(tc.core).DeclaredMatchesImplemented()
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

func TestARealRoutingDeclarationIsAccepted(t *testing.T) {
	base := routingFake{kinds: []Kind{"fk"}}

	for _, tc := range []struct {
		name string
		core Core
		deny string
	}{
		{"an ingress that routes a kind", ingressFake{routingFake: base, selector: IngressDevice}, "RoutableIngress"},
		{"an egress that offers a kind", egressFake{routingFake: base, exits: []Kind{"fk"}}, "RoutableEgress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, problem := range Bind(tc.core).DeclaredMatchesImplemented() {
				if strings.Contains(problem, tc.deny) {
					t.Fatalf("a real declaration must not be reported: %q", problem)
				}
			}
		})
	}
}

/*
A core that provisions users for no kind it serves declares nothing.

Same rule as the routing ones above: implementing the interface is a claim, and
a claim that applies to none of the core's own kinds is the same silence as not
implementing it at all. F314.
*/
type provisionerFake struct {
	routingFake
	provisions bool
}

func (f provisionerFake) ProvisionsUsers(Kind) bool { return f.provisions }
func (provisionerFake) AddUser(context.Context, Instance, User) error {
	return nil
}
func (provisionerFake) RemoveUser(context.Context, Instance, string) error { return nil }

func TestAProvisionerThatServesNoUserKindIsAConformanceFailure(t *testing.T) {
	base := routingFake{kinds: []Kind{"fk"}}
	const want = "UserProvisioner: implemented, but every kind answers ProvisionsUsers false"

	problems := Bind(provisionerFake{routingFake: base}).DeclaredMatchesImplemented()
	if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
		t.Fatalf("problems = %v, want one containing %q", problems, want)
	}

	// And a core that does provision one is not reported.
	for _, problem := range Bind(provisionerFake{routingFake: base, provisions: true}).DeclaredMatchesImplemented() {
		if strings.Contains(problem, "UserProvisioner") {
			t.Fatalf("a real declaration must not be reported: %q", problem)
		}
	}
}

/*
A core cannot declare a criterion the panel has no way to produce.

This is the guard the whole multi-core routing promise rests on. The criteria
mask says: a rule the editor lets you write is a rule that can match. A payload
criterion -- domain, protocol, attrs -- is only answerable where the panel has
an Xray inbound to hang a sniffer on: the one an internal ingress already
answers to, or the front it synthesises for a device.

Every shape the vocabulary names has one today. A selector outside it does not,
and outside it is exactly where the next one arrives: the vocabulary is closed,
so an unrecognised value reads as "cannot route" and nothing can sniff it. A
core shipping one and declaring `domain` would produce rules that save, display
as active, and never fire -- F060 written again by the next core author, in a
package nobody reviewing routing would look at.

Caught in their own conformance run instead. The day the panel learns to sniff a
new shape, InspectableSelector gains it and this stops firing for that shape.
*/
type unmechanizedIngressFake struct {
	routingFake
	criteria []Criterion
}

func (unmechanizedIngressFake) IngressSelector(Kind) IngressSelector {
	return IngressSelector("carrier-pigeon")
}

func (unmechanizedIngressFake) IngressHandle(context.Context, Instance) (IngressHandle, error) {
	return IngressHandle{}, nil
}
func (f unmechanizedIngressFake) IngressCriteria(Kind) []Criterion { return f.criteria }

func TestACoreCannotDeclareACriterionThePanelCannotProduce(t *testing.T) {
	base := routingFake{kinds: []Kind{"ppp"}}

	withDomain := unmechanizedIngressFake{
		routingFake: base,
		criteria:    append(CriteriaByAddress(), CriterionDomain),
	}
	problems := Bind(withDomain).DeclaredMatchesImplemented()
	const want = "declares criterion domain"
	if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
		t.Fatalf("an unsniffable ingress declaring a domain criterion passed conformance: %v", problems)
	}

	// The same core routing by address alone is exactly right, and must pass.
	byAddress := unmechanizedIngressFake{routingFake: base, criteria: CriteriaByAddress()}
	for _, problem := range Bind(byAddress).DeclaredMatchesImplemented() {
		if strings.Contains(problem, "declares criterion") {
			t.Fatalf("an address-only unsniffable ingress was refused: %q", problem)
		}
	}
}

// A pool ingress is sniffable now, because its traffic crosses the same
// synthesised front a device's does. It may name the domain like any other.
func TestAPoolIngressMayDeclareTheDomain(t *testing.T) {
	if !InspectableSelector(IngressSubnet) {
		t.Fatal("IngressSubnet is not inspectable, so a rule on an OpenConnect inbound could not match a domain")
	}
}

// And an inspectable shape may declare it freely: that is the whole point.
func TestAnInspectableShapeMayDeclareTheDomain(t *testing.T) {
	fake := ingressFake{
		routingFake: routingFake{kinds: []Kind{"fk"}},
		selector:    IngressDevice,
	}
	for _, problem := range Bind(fake).DeclaredMatchesImplemented() {
		if strings.Contains(problem, "declares criterion") {
			t.Fatalf("a device ingress was refused a criterion the panel can produce: %q", problem)
		}
	}
}
