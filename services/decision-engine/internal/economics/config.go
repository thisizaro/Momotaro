// Package economics loads the checked-in cost model and recovery priors and
// turns them into expected values (score.go). It is the deterministic half of
// docs/ARCHITECTURE.md section 5a: the Classifier proposes, the guardrails
// constrain, and this decides. Nothing here is a model call, which is what
// lets us say an LLM never decides how money is spent.
//
// This file does one job: read the two YAML files and validate them. The
// maths lives in score.go (docs/ENGINEERING.md section 14).
package economics

import (
	"fmt"
	"os"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"gopkg.in/yaml.v3"
)

// actionCost is what one intervention costs, split the way
// configs/intervention_costs.yaml splits it. IndirectPaise prices the
// authorization-rate damage from repeated card retries, a real cost that
// appears on no invoice.
type actionCost struct {
	DirectPaise   int64 `yaml:"direct_cost_paise"`
	IndirectPaise int64 `yaml:"indirect_cost_paise"`
}

// attemptPriors is one (action, bucket) row of the prior table. Either the
// per-attempt fields are set, or AllAttempts pins every attempt to one value,
// which is how the config expresses "this can never work" as an exact zero
// rather than a small number.
type attemptPriors struct {
	Attempt1    *int `yaml:"attempt_1"`
	Attempt2    *int `yaml:"attempt_2"`
	Attempt3    *int `yaml:"attempt_3"`
	AllAttempts *int `yaml:"all_attempts"`
}

type costsFile struct {
	Actions map[string]actionCost `yaml:"actions"`
}

type priorsFile struct {
	BeyondListedAttemptsBps int                                 `yaml:"beyond_listed_attempts_bps"`
	PriorsBps               map[string]map[string]attemptPriors `yaml:"priors_bps"`
}

// Model is the loaded economics, keyed by proto enum so nothing downstream
// handles config strings.
type Model struct {
	costs                   map[commonv1.ActionType]actionCost
	priors                  map[commonv1.ActionType]map[commonv1.RootCauseBucket]attemptPriors
	beyondListedAttemptsBps int
}

// Load reads the cost model and the prior table.
//
// The files are checked in rather than compiled in specifically so the numbers
// can be read and argued with, so a parse or naming error must fail loudly at
// startup: a Model that silently loaded nothing would score every action at
// zero and close every record as uneconomic, which looks like a working agent
// that has decided nothing is ever worth doing.
func Load(costsPath, priorsPath string) (*Model, error) {
	var costs costsFile
	if err := readYAML(costsPath, &costs); err != nil {
		return nil, err
	}
	var priors priorsFile
	if err := readYAML(priorsPath, &priors); err != nil {
		return nil, err
	}

	m := &Model{
		costs:                   make(map[commonv1.ActionType]actionCost, len(costs.Actions)),
		priors:                  make(map[commonv1.ActionType]map[commonv1.RootCauseBucket]attemptPriors, len(priors.PriorsBps)),
		beyondListedAttemptsBps: priors.BeyondListedAttemptsBps,
	}

	for name, cost := range costs.Actions {
		action, err := actionFromConfigName(name)
		if err != nil {
			return nil, fmt.Errorf("%s: actions: %w", costsPath, err)
		}
		m.costs[action] = cost
	}

	for actionName, byBucket := range priors.PriorsBps {
		action, err := actionFromConfigName(actionName)
		if err != nil {
			return nil, fmt.Errorf("%s: priors_bps: %w", priorsPath, err)
		}
		m.priors[action] = make(map[commonv1.RootCauseBucket]attemptPriors, len(byBucket))
		for bucketName, row := range byBucket {
			bucket, err := bucketFromConfigName(bucketName)
			if err != nil {
				return nil, fmt.Errorf("%s: priors_bps.%s: %w", priorsPath, actionName, err)
			}
			m.priors[action][bucket] = row
		}
	}

	if len(m.costs) == 0 || len(m.priors) == 0 {
		return nil, fmt.Errorf("economics config loaded empty: costs=%d priors=%d", len(m.costs), len(m.priors))
	}
	return m, nil
}

func readYAML(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// actionFromConfigName maps the config's short key onto the proto enum. The
// config documents this correspondence in its own enum_mapping block; deriving
// it from the prefix rather than hardcoding a table means a typo in either
// file is an error here instead of a silently missing row that would score as
// zero.
func actionFromConfigName(name string) (commonv1.ActionType, error) {
	value, ok := commonv1.ActionType_value["ACTION_TYPE_"+name]
	if !ok {
		return 0, fmt.Errorf("unknown action %q, expected one of the ActionType enum short names", name)
	}
	return commonv1.ActionType(value), nil
}

func bucketFromConfigName(name string) (commonv1.RootCauseBucket, error) {
	value, ok := commonv1.RootCauseBucket_value["ROOT_CAUSE_BUCKET_"+name]
	if !ok {
		return 0, fmt.Errorf("unknown bucket %q, expected one of the RootCauseBucket enum short names", name)
	}
	return commonv1.RootCauseBucket(value), nil
}
