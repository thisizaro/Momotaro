package hopcodec

import (
	"strings"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func hops(pairs ...string) []*commonv1.ProviderHop {
	out := make([]*commonv1.ProviderHop, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, &commonv1.ProviderHop{Provider: pairs[i], Result: pairs[i+1]})
	}
	return out
}

// The reason this package exists: two services, two directions, one format.
// Iterate the whole closed vocabulary so a value added to provider.Hop*
// without being round-trippable fails here.
func TestRoundTripOverTheWholeVocabulary(t *testing.T) {
	providers := []string{"groq", "gemini", "rules"}
	results := []string{"ok", "error", "timeout", "rate_limited", "schema_invalid", "circuit_open", "deadline_exhausted"}

	for _, p := range providers {
		for _, r := range results {
			in := hops(p, r)
			encoded, err := Encode(in)
			if err != nil {
				t.Fatalf("Encode(%s/%s): %v", p, r, err)
			}
			out, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode(%q): %v", encoded, err)
			}
			if len(out) != 1 || out[0].GetProvider() != p || out[0].GetResult() != r {
				t.Errorf("round trip of %s/%s gave %+v", p, r, out)
			}
		}
	}
}

func TestRoundTripPreservesOrder(t *testing.T) {
	in := hops("groq", "timeout", "gemini", "rate_limited", "rules", "ok")
	encoded, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded != "groq:timeout,gemini:rate_limited,rules:ok" {
		t.Errorf("Encode = %q, want the documented format", encoded)
	}
	out, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("Decode gave %d hops, want 3", len(out))
	}
	// Order is the whole story a hop list tells: which was tried first.
	for i, want := range []string{"groq", "gemini", "rules"} {
		if out[i].GetProvider() != want {
			t.Errorf("hop %d = %q, want %q: attempt order must survive storage", i, out[i].GetProvider(), want)
		}
	}
}

// NULL and "a classification that tried nothing" are different facts.
func TestEmptyAndNilEncodeToTheEmptyString(t *testing.T) {
	for name, in := range map[string][]*commonv1.ProviderHop{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Encode(in)
			if err != nil {
				t.Fatalf("Encode(%s): %v", name, err)
			}
			if got != "" {
				t.Errorf("Encode(%s) = %q, want the empty string so callers store NULL", name, got)
			}
		})
	}
	out, err := Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\"): %v", err)
	}
	if out != nil {
		t.Errorf("Decode(\"\") = %+v, want nil", out)
	}
}

// A delimiter-based format whose delimiter case is untested is a bug waiting
// for the first provider named "groq:v2". provider.NewChain rejects these at
// startup; Encode refuses them anyway, because the cost of being wrong here is
// a corrupted audit row rather than a failed boot.
func TestEncodeRefusesDelimitersInEitherField(t *testing.T) {
	cases := map[string][]*commonv1.ProviderHop{
		"colon in provider": hops("groq:v2", "ok"),
		"comma in provider": hops("groq,gemini", "ok"),
		"colon in result":   hops("groq", "ok:ish"),
		"comma in result":   hops("groq", "ok,fine"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Encode(in); err == nil {
				t.Errorf("Encode(%s): want error, got nil", name)
			}
		})
	}
}

func TestEncodeRefusesAHalfEmptyHop(t *testing.T) {
	for name, in := range map[string][]*commonv1.ProviderHop{
		"no provider": hops("", "ok"),
		"no result":   hops("groq", ""),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Encode(in); err == nil {
				t.Errorf("Encode(%s): want error, got nil", name)
			}
		})
	}
}

// A trail that quietly loses a hop is worse than one that admits it cannot be
// read: the entire point of the field is to show what was tried.
func TestDecodeRefusesMalformedInputRatherThanDroppingHops(t *testing.T) {
	for _, in := range []string{"groq", "groq:ok,gemini", ":ok", "groq:", "groq:ok,,rules:ok"} {
		t.Run(in, func(t *testing.T) {
			if _, err := Decode(in); err == nil {
				t.Errorf("Decode(%q): want error, got nil", in)
			}
		})
	}
}

func TestDecodeErrorNamesTheOffendingInput(t *testing.T) {
	_, err := Decode("groq:ok,gemini")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error = %q, want it to name the malformed pair", err)
	}
}
