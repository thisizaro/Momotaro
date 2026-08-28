package llm

import (
	"strings"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// The schema is derived from the proto enums so a bucket added to
// common.proto cannot silently fail to reach the model. Assert the derivation,
// not a hardcoded list, or this test would need editing for the exact change
// it exists to catch.
func TestBucketNamesCoverEveryRootCauseBucket(t *testing.T) {
	got := bucketNames()
	if len(got) != len(commonv1.RootCauseBucket_name) {
		t.Fatalf("bucketNames() has %d entries, RootCauseBucket has %d: a bucket was added to the proto without reaching the model",
			len(got), len(commonv1.RootCauseBucket_name))
	}
	for _, name := range commonv1.RootCauseBucket_name {
		if !contains(got, name) {
			t.Errorf("bucket %q is missing from the schema", name)
		}
	}
	// UNSPECIFIED stays offered on purpose: "we cannot tell" is a real
	// diagnosis and the rules engine emits it for an unknown failure code.
	if !contains(got, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED.String()) {
		t.Error("UNSPECIFIED bucket should be offered: it is the honest answer for an undiagnosable record")
	}
}

func TestActionNamesWithholdTheTwoActionsAModelMustNotChoose(t *testing.T) {
	got := actionNames()

	// NONE is an economics conclusion (ClosedUneconomic), not a diagnosis.
	// A model choosing it would be pricing an action, which PRD.md section 2a
	// forbids.
	if contains(got, commonv1.ActionType_ACTION_TYPE_NONE.String()) {
		t.Error("ACTION_TYPE_NONE must not be offered: closing a record on economics grounds is the Decision Engine's call")
	}
	// Uncertainty already has an answer in this menu, and it is ESCALATE.
	if contains(got, commonv1.ActionType_ACTION_TYPE_UNSPECIFIED.String()) {
		t.Error("ACTION_TYPE_UNSPECIFIED must not be offered: an unsure model should escalate")
	}
	for _, want := range []commonv1.ActionType{
		commonv1.ActionType_ACTION_TYPE_RETRY,
		commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		commonv1.ActionType_ACTION_TYPE_ESCALATE,
	} {
		if !contains(got, want.String()) {
			t.Errorf("action %q should be offered", want.String())
		}
	}
}

// Groq's strict mode rejects a schema without additionalProperties; Gemini
// rejects one with it. Getting this backwards fails at request time against a
// real provider and never in a fake, so it is worth asserting directly.
func TestOutputSchemaDialectsDifferOnAdditionalProperties(t *testing.T) {
	strict := outputSchema(dialectStrictJSONSchema)
	if _, ok := strict["additionalProperties"]; !ok {
		t.Error("strict dialect must set additionalProperties: Groq rejects a strict schema without it")
	}
	gemini := outputSchema(dialectGemini)
	if _, ok := gemini["additionalProperties"]; ok {
		t.Error("gemini dialect must omit additionalProperties: it is outside Gemini's supported JSON Schema subset")
	}
}

func TestOutputSchemaRequiresEveryField(t *testing.T) {
	s := outputSchema(dialectStrictJSONSchema)
	required, _ := s["required"].([]string)
	for _, f := range []string{"bucket", "recommended_action", "rationale", "confidence"} {
		if !contains(required, f) {
			t.Errorf("%q missing from required; Groq strict mode needs every field required", f)
		}
	}
	props, _ := s["properties"].(map[string]any)
	if len(props) != len(required) {
		t.Errorf("properties (%d) and required (%d) disagree", len(props), len(required))
	}
}

func TestOutputSchemaIsDeterministic(t *testing.T) {
	// Enum name maps iterate randomly in Go. A schema whose enum order shifts
	// between pods is a schema that busts any provider-side cache and makes
	// two identical records look different on the wire.
	first := strings.Join(bucketNames(), ",")
	for i := 0; i < 50; i++ {
		if got := strings.Join(bucketNames(), ","); got != first {
			t.Fatalf("bucketNames() is not deterministic:\n first: %s\n got:   %s", first, got)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
