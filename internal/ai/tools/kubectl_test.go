package tools

import "testing"

func TestSplitCommandLineKeepsQuotedArgs(t *testing.T) {
	args, err := splitCommandLine(`kubectl set image deployment/product-catalog product-catalog="ghcr.io/test/image:v1" -n astronomy-shop`)
	if err != nil {
		t.Fatalf("split command: %v", err)
	}
	expected := []string{
		"kubectl",
		"set",
		"image",
		"deployment/product-catalog",
		"product-catalog=ghcr.io/test/image:v1",
		"-n",
		"astronomy-shop",
	}
	if len(args) != len(expected) {
		t.Fatalf("expected %#v, got %#v", expected, args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Fatalf("expected %#v, got %#v", expected, args)
		}
	}
}

func TestKubectlCommandValidation(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get pods -n astronomy-shop",
		"kubectl describe deployment product-catalog -n astronomy-shop",
		"kubectl logs deployment/product-catalog -n astronomy-shop",
		"kubectl rollout status deployment/product-catalog -n astronomy-shop",
	} {
		args, err := splitCommandLine(cmd)
		if err != nil {
			t.Fatalf("split %q: %v", cmd, err)
		}
		if err := validateKubectlArgs(args[1:]); err != nil {
			t.Fatalf("expected %q to be allowed: %v", cmd, err)
		}
	}

	for _, cmd := range []string{
		"kubectl delete namespace astronomy-shop",
		"kubectl get pods -n astronomy-shop | grep product",
		"kubectl exec product-catalog -- sh",
	} {
		if containsShellControl(cmd) {
			continue
		}
		args, err := splitCommandLine(cmd)
		if err != nil {
			t.Fatalf("split %q: %v", cmd, err)
		}
		if err := validateKubectlArgs(args[1:]); err == nil {
			t.Fatalf("expected %q to be denied", cmd)
		}
	}
}
