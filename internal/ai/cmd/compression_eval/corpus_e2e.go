package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	chatanswer "github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline/answer"
	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline/execution"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/knowledge"
	kc "github.com/Thadopz/telltale/internal/ai/knowledgecompression"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

func endToEndFrozenCorpus(ctx context.Context, output, inputPath, split string) error {
	if _, err := os.Stat(filepath.Join(output, "complete")); err != nil {
		return fmt.Errorf("index incomplete: %w", err)
	}
	if split == "holdout" {
		if _, err := os.Stat(filepath.Join(output, "e2e-dev.complete")); err != nil {
			return fmt.Errorf("development e2e incomplete: %w", err)
		}
	}
	inputs, err := loadDatasetInputs(inputPath)
	if err != nil {
		return err
	}
	var subsets struct {
		Subsets struct {
			EndToEnd []string `json:"end_to_end_holdout"`
		} `json:"subsets"`
	}
	raw, err := os.ReadFile(filepath.Join(corpusDirectory, "assessment", "cases.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &subsets); err != nil {
		return err
	}
	if len(subsets.Subsets.EndToEnd) != 8 {
		return fmt.Errorf("invalid fixed e2e subset")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	database := filepath.Join(output, "experiment.db")
	if info, err := os.Stat(database); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("isolated index database is missing")
	}
	appconfig.Configure(ctx)
	if err := os.Setenv("SUPERBIZ_SQLITE_PATH", database); err != nil {
		return err
	}
	if err := storage.Initialize(ctx); err != nil {
		return err
	}
	defer g.DB().Close(ctx)
	ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: "compression-eval", TenantID: "isolated-eval", Roles: []string{"oncall"}})
	ctx = authz.WithAgentScope(ctx, authz.AgentScopeKnowledge)
	ctx = evidence.WithRunID(ctx, "corpus-e2e")
	ctx = knowledge.WithRerankDisabled(ctx)
	registry := tools.NewRegistry()
	knowledgeTools, err := registry.ToolsForAgent(ctx, tools.ScenarioChat, authz.AgentScopeKnowledge)
	if err != nil {
		return err
	}
	retry := 0
	runner, err := execution.BuildChatAgentWithOptions(ctx, execution.ChatAgentOptions{Tools: knowledgeTools, AgentScope: authz.AgentScopeKnowledge, AllowedDomains: []tools.Domain{tools.DomainKnowledge}, MaxIterations: 12, ModelRetries: &retry, DisableToolSearch: true})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(output, "e2e-"+split+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, input := range inputs {
		if input.Split != split || split == "holdout" && !slices.Contains(subsets.Subsets.EndToEnd, input.ID) {
			continue
		}
		for repeat := 1; repeat <= 3; repeat++ {
			for _, policy := range []string{"baseline", "task"} {
				session := fmt.Sprintf("%s-%s-%d", input.ID, policy, repeat)
				var history []*schema.Message
				for index, turn := range input.Turns {
					recorder := trace.NewRecorder()
					started := time.Now()
					runCtx, cancel := context.WithTimeout(context.WithValue(kc.WithGoal(kc.WithPolicy(ctx, policy), turn.User, ""), "client_id", session), 120*time.Second)
					draft, invokeErr := runner.Invoke(runCtx, &execution.UserMessage{UserID: "compression-eval", ConversationID: session, Query: turn.User, History: history}, compose.WithCallbacks(trace.Callback("corpus-e2e", recorder)))
					pipeline := &chatanswer.ClaimPipelineResult{}
					answer := ""
					if invokeErr == nil && draft != nil {
						answer, invokeErr = chatanswer.ConvergeAnswer(runCtx, chatanswer.AnswerConvergeInput{Question: turn.User, Answer: draft.Content, Steps: recorder.Steps(), ToolEvidence: recorder.ToolEvidence(), Recorder: recorder, PipelineResult: pipeline})
					} else if invokeErr == nil {
						invokeErr = fmt.Errorf("empty agent response")
					}
					cancel()
					row := map[string]any{"case_id": input.ID, "split": split, "turn": index + 1, "repeat": repeat, "strategy": policy, "answer": answer, "draft": draft, "trace": recorder.Steps(), "tool_evidence": recorder.ToolEvidence(), "claim_pipeline": pipeline, "elapsed_ms": time.Since(started).Milliseconds(), "usage": "see trace; unavailable values are unknown"}
					if invokeErr != nil {
						row["error"] = invokeErr.Error()
					}
					if err := encoder.Encode(row); err != nil {
						return err
					}
					if err := file.Sync(); err != nil {
						return err
					}
					if invokeErr != nil {
						if strings.Contains(invokeErr.Error(), "402") || strings.Contains(invokeErr.Error(), "Insufficient Balance") {
							return invokeErr
						}
						break
					}
					history = append(history, schema.UserMessage(turn.User), schema.AssistantMessage(answer, nil))
					fmt.Printf("e2e %s turn=%d repeat=%d strategy=%s\n", input.ID, index+1, repeat, policy)
				}
			}
		}
	}
	return os.WriteFile(filepath.Join(output, "e2e-"+split+".complete"), []byte("complete\n"), 0o600)
}
