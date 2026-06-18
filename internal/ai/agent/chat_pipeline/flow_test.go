package chat_pipeline

import (
	"SREagent/internal/authz"
	"context"
	"testing"
)

func TestDefaultReactMaxStepForSREGymRole(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "sregym", Roles: []string{"sregym"}})
	if got := defaultReactMaxStep(ctx); got != 50 {
		t.Fatalf("expected SREGym max step 50, got %d", got)
	}
}

func TestDefaultReactMaxStepForNormalRole(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "user", Roles: []string{"oncall"}})
	if got := defaultReactMaxStep(ctx); got != 25 {
		t.Fatalf("expected normal max step 25, got %d", got)
	}
}
