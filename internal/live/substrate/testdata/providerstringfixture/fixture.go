// Package fixture is a known-shape tree for providerstring_test.go's
// TestProviderStringGuardSeesTheFixture: one site per detection shape, none
// of them in providerStringAllowed or providerStringPendingRound2, so the
// guard's own test can prove the scanner names each one.
package fixture

import "strings"

func decide(provider string) bool {
	return provider == "aws"
}

func prefixCheck(typeName string) bool {
	return strings.HasPrefix(typeName, "kubernetes_")
}

func switchOnKind(kind string) string {
	switch kind {
	case "kubernetes":
		return "cluster"
	default:
		return "other"
	}
}

var familyHandlers = map[string]func(){
	"aws": func() {},
}
