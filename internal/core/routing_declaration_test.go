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
