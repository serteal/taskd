package server

import (
	"context"
	"errors"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/rules"
	"todoapp/internal/store"
)

type ruleService struct {
	taskcorev1.UnimplementedRuleServiceServer
	s *Server
}

// dryRunMatchCap bounds the items returned by DryRunRule; total_matches
// still reports the full count (see rule_service.proto).
const dryRunMatchCap = 100

func (r *ruleService) SaveRule(ctx context.Context, req *taskcorev1.SaveRuleRequest) (*taskcorev1.SaveRuleResponse, error) {
	rule := req.GetRule()
	// Validate is shared with the engine's load path, so a rule that saves
	// is a rule that runs — a broken rule fails here, not at fire time.
	if err := rules.Validate(r.s.eng, rule); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := r.s.st.SaveRule(ctx, rule); err != nil {
		return nil, storeErr(err)
	}
	// Re-read so the response carries the resolved position (SaveRule with
	// position 0 appends after the current maximum).
	saved, err := r.s.st.GetRule(ctx, rule.GetName())
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.SaveRuleResponse{Rule: saved}, nil
}

func (r *ruleService) GetRule(ctx context.Context, req *taskcorev1.GetRuleRequest) (*taskcorev1.GetRuleResponse, error) {
	rule, err := r.s.st.GetRule(ctx, req.GetName())
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.GetRuleResponse{Rule: rule}, nil
}

func (r *ruleService) ListRules(ctx context.Context, _ *taskcorev1.ListRulesRequest) (*taskcorev1.ListRulesResponse, error) {
	list, err := r.s.st.ListRules(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.ListRulesResponse{Rules: list}, nil
}

func (r *ruleService) DeleteRule(ctx context.Context, req *taskcorev1.DeleteRuleRequest) (*taskcorev1.DeleteRuleResponse, error) {
	// The store's DELETE is a silent no-op for absent names; surface
	// NotFound so a typo'd `task rule rm` doesn't claim success.
	if _, err := r.s.st.GetRule(ctx, req.GetName()); err != nil {
		return nil, storeErr(err)
	}
	if err := r.s.st.DeleteRule(ctx, req.GetName()); err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.DeleteRuleResponse{}, nil
}

// DryRunRule evaluates the rule's condition as a LEVEL over current items:
// "what would match right now". No edge semantics, no writes; works on
// unsaved rules.
func (r *ruleService) DryRunRule(ctx context.Context, req *taskcorev1.DryRunRuleRequest) (*taskcorev1.DryRunRuleResponse, error) {
	rule := req.GetRule()
	// A broken rule must not scan the store.
	if err := rules.Validate(r.s.eng, rule); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	cond := rule.GetBecame()
	if rule.GetSchedule() != nil {
		cond = rule.GetWhere()
	}
	compiled, err := r.s.eng.Compile(cond)
	if err != nil {
		// Validate compiled the same expression already; belt and suspenders.
		return nil, status.Errorf(codes.InvalidArgument, "condition: %v", err)
	}
	var matches []*taskcorev1.Item
	total := 0
	token := ""
	for {
		res, err := r.s.st.QueryItems(ctx, store.Query{
			Where:    compiled.Where,
			Args:     compiled.Args,
			Residual: compiled.Residual,
			OrderBy:  "created_at",
			PageSize: 500, PageToken: token,
		})
		if err != nil {
			return nil, storeErr(err)
		}
		total += len(res.Items)
		for _, it := range res.Items {
			if len(matches) < dryRunMatchCap {
				matches = append(matches, it)
			}
		}
		if res.NextPageToken == "" {
			break
		}
		token = res.NextPageToken
	}
	return &taskcorev1.DryRunRuleResponse{Matches: matches, TotalMatches: int32(total)}, nil
}

// BackfillRule applies a saved rule once to every currently matching item —
// the explicit opt-in that keeps rule edits non-retroactive by default.
func (r *ruleService) BackfillRule(ctx context.Context, req *taskcorev1.BackfillRuleRequest) (*taskcorev1.BackfillRuleResponse, error) {
	n, err := r.s.backfill(ctx, req.GetName())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rule %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "backfill: %v", err)
	}
	return &taskcorev1.BackfillRuleResponse{Applied: int32(n)}, nil
}

// ListRuleTemplates flattens the rule templates of every persisted plugin
// manifest — proposals to copy into user rules, never enforced. Manifests
// outlive their plugins, so templates from uninstalled plugins still list.
func (r *ruleService) ListRuleTemplates(ctx context.Context, _ *taskcorev1.ListRuleTemplatesRequest) (*taskcorev1.ListRuleTemplatesResponse, error) {
	manifests, err := r.s.st.ListManifests(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	var out []*taskcorev1.RuleTemplate
	for _, m := range manifests {
		for _, rule := range m.GetRuleTemplates() {
			out = append(out, &taskcorev1.RuleTemplate{Plugin: m.GetName(), Rule: rule})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].GetPlugin() != out[j].GetPlugin() {
			return out[i].GetPlugin() < out[j].GetPlugin()
		}
		return out[i].GetRule().GetName() < out[j].GetRule().GetName()
	})
	return &taskcorev1.ListRuleTemplatesResponse{Templates: out}, nil
}
