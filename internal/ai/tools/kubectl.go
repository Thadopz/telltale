package tools

import (
	"SREagent/internal/ai/evidence"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const kubectlToolName = "exec_kubectl_cmd_safely"

type KubectlCommandInput struct {
	Cmd string `json:"cmd" jsonschema:"description=Complete kubectl command to execute, for example kubectl get pods -n astronomy-shop or kubectl describe pod POD -n astronomy-shop. Do not use shell pipes or redirection."`
}

func NewSREGymKubectlTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		kubectlToolName,
		"Execute a single kubectl command against the current SREGym Kubernetes cluster. Use this for Kubernetes pods, deployments, namespaces, events, logs, rollouts, and safe image updates. Do not use internal documentation tools for kubectl commands.",
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
	switch verb {
	case "get", "describe", "logs", "top", "events", "api-resources", "api-versions":
		return nil
	case "rollout":
		if len(args) >= 2 {
			action := strings.ToLower(args[1])
			if action == "status" || action == "history" || action == "undo" {
				return nil
			}
		}
	case "set":
		if len(args) >= 2 && strings.EqualFold(args[1], "image") {
			return nil
		}
	case "patch":
		if len(args) >= 2 {
			resource := strings.ToLower(args[1])
			if resource == "deployment" || resource == "deploy" || strings.HasPrefix(resource, "deployment/") || strings.HasPrefix(resource, "deploy/") {
				return nil
			}
		}
	}
	return fmt.Errorf("kubectl verb %q is not allowed by this SREGym tool", strings.Join(args, " "))
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
