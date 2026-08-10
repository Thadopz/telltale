package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const kubectlToolName = "exec_kubectl_cmd_safely"

type KubectlCommandInput struct {
	Cmd string `json:"cmd" jsonschema:"description=Read-only kubectl command to execute, for example kubectl get pods -n astronomy-shop or kubectl describe pod POD -n astronomy-shop. Mutations, shell operators, credential overrides, and local file flags are rejected."`
}

func NewSREGymKubectlTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		kubectlToolName,
		"Execute one read-only kubectl command against the current SREGym Kubernetes cluster. Use this for pods, deployments, namespaces, events, logs, and rollout status. Mutations require a separate confirmed operator path and are not available through this tool.",
		func(ctx context.Context, input *KubectlCommandInput, opts ...tool.Option) (string, error) {
			out, err := runKubectlCommand(ctx, input)
			if err != nil {
				return marshalToolError(err, "kubectl command failed"), nil
			}
			out = truncateSREGymToolOutput(out, 3500)
			return marshalToolEvidence(out, "kubectl command returned output", []evidence.Observation{{
				Kind: evidence.ObservationMetricValue,
				Subject: evidence.SubjectRef{
					Type:    "kubernetes",
					Service: "sregym",
					Fields:  map[string]string{"tool": kubectlToolName},
				},
				Fields:  map[string]any{"tool": kubectlToolName, "cmd": strings.TrimSpace(input.Cmd)},
				Summary: "kubectl command returned output",
			}}), nil
		},
	)
}

func truncateSREGymToolOutput(out string, maxRunes int) string {
	if maxRunes <= 0 {
		return out
	}
	runes := []rune(out)
	if len(runes) <= maxRunes {
		return out
	}
	return string(runes[:maxRunes]) + "\n...[truncated kubectl output]"
}

func runKubectlCommand(ctx context.Context, input *KubectlCommandInput) (string, error) {
	if input == nil {
		return "", errors.New("missing kubectl input")
	}
	raw := strings.TrimSpace(input.Cmd)
	if raw == "" {
		return "", errors.New("missing kubectl command")
	}
	if containsShellControl(raw) {
		return "", fmt.Errorf("kubectl command must not contain shell control operators")
	}
	argv, err := splitCommandLine(raw)
	if err != nil {
		return "", err
	}
	if len(argv) == 0 || argv[0] != "kubectl" {
		return "", fmt.Errorf("command must start with kubectl")
	}
	if err := validateKubectlArgs(argv[1:]); err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "kubectl", argv[1:]...)
	output, execErr := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))
	if runCtx.Err() != nil {
		return out, fmt.Errorf("kubectl command timed out: %w", runCtx.Err())
	}
	if execErr != nil {
		if out == "" {
			return "", execErr
		}
		return out, fmt.Errorf("%w: %s", execErr, out)
	}
	if out == "" {
		out = "(kubectl command completed with no output)"
	}
	return out, nil
}

func containsShellControl(cmd string) bool {
	for _, forbidden := range []string{"&&", "||", "|", ";", ">", "<", "`", "$("} {
		if strings.Contains(cmd, forbidden) {
			return true
		}
	}
	return false
}

func validateKubectlArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("missing kubectl verb")
	}
	verb := strings.ToLower(args[0])
	if err := validateKubectlFlags(verb, args[1:]); err != nil {
		return err
	}
	switch verb {
	case "get", "describe", "logs", "top", "events", "api-resources", "api-versions":
		return nil
	case "rollout":
		if len(args) >= 2 {
			action := strings.ToLower(args[1])
			if action == "status" || action == "history" {
				return nil
			}
		}
	}
	return fmt.Errorf("kubectl verb %q is not allowed by this SREGym tool", strings.Join(args, " "))
}

func validateKubectlFlags(verb string, args []string) error {
	for index, argument := range args {
		lower := strings.ToLower(strings.TrimSpace(argument))
		flagName := lower
		if equals := strings.Index(flagName, "="); equals >= 0 {
			flagName = flagName[:equals]
		}

		switch flagName {
		case "--kubeconfig", "--server", "--token", "--username", "--password",
			"--client-key", "--client-certificate", "--certificate-authority",
			"--as", "--as-group", "--as-uid", "--context", "--cluster", "--user",
			"--cache-dir", "--log-file", "--log-dir", "--filename", "--kustomize", "--raw":
			return fmt.Errorf("kubectl flag %q is not allowed", flagName)
		case "-k":
			return fmt.Errorf("kubectl local file flag %q is not allowed", flagName)
		case "-f":
			if verb != "logs" {
				return fmt.Errorf("kubectl local file flag %q is not allowed", flagName)
			}
		}

		if outputUsesLocalFile(lower) {
			return fmt.Errorf("kubectl output must not read a local template file")
		}
		if (lower == "-o" || lower == "--output") && index+1 < len(args) && outputUsesLocalFile(args[index+1]) {
			return fmt.Errorf("kubectl output must not read a local template file")
		}
	}
	return nil
}

func outputUsesLocalFile(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.Contains(value, "go-template-file") || strings.Contains(value, "jsonpath-file")
}

func splitCommandLine(s string) ([]string, error) {
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if b.Len() > 0 {
				args = append(args, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in kubectl command")
	}
	if b.Len() > 0 {
		args = append(args, b.String())
	}
	return args, nil
}
