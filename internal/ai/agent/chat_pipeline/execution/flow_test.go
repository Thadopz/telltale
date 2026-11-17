package execution

import (
	"context"
	"github.com/Thadopz/telltale/internal/authz"
	"testing"
)

func TestDefaultMaxIterationsForSREGymRole(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "sregym", Roles: []string{"sregym"}})
	if got := defaultMaxIterations(ctx); got != 50 {
		t.Fatalf("expected SREGym max step 50, got %d", got)
	}
}

func TestDefaultMaxIterationsForNormalRole(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "user", Roles: []string{"oncall"}})
	if got := defaultMaxIterations(ctx); got != 25 {
		t.Fatalf("expected normal max step 25, got %d", got)
	}
}

func TestToolSearchStartsAtDefaultToolThreshold(t *testing.T) {
	ctx := context.Background()
	if shouldUseToolSearch(ctx, 7) {
		t.Fatal("expected small tool set to stay directly visible")
	}
	if !shouldUseToolSearch(ctx, 8) {
		t.Fatal("expected large tool set to use ADK tool search")
	}
}
