package documentpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const MetadataKey = "document_policy"

type Record struct {
	State        string `json:"state"`
	Version      string `json:"version,omitempty"`
	ValidFrom    string `json:"valid_from,omitempty"`
	ValidUntil   string `json:"valid_until,omitempty"`
	VerifiedAt   string `json:"verified_at,omitempty"`
	VerifiedBy   string `json:"verified_by,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

func Decode(reader io.Reader) (Record, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil {
		return Record{}, err
	}
	if len(data) > 65536 {
		return Record{}, fmt.Errorf("document metadata exceeds 64KiB")
	}
	var record Record
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return record, fmt.Errorf("extra document metadata")
	}
	return record.normalize()
}

func FromMetadata(metadata map[string]any) (Record, error) {
	if value, ok := metadata[MetadataKey]; ok {
		data, err := json.Marshal(value)
		if err != nil {
			return Record{}, err
		}
		return Decode(strings.NewReader(string(data)))
	}
	return Record{State: "unverified"}, nil
}

func (r Record) normalize() (Record, error) {
	if r.State == "" {
		r.State = "unverified"
	}
	switch r.State {
	case "unverified", "current", "planned", "deprecated":
	default:
		return r, fmt.Errorf("invalid document state %q", r.State)
	}
	for _, value := range []*string{&r.ValidFrom, &r.ValidUntil, &r.VerifiedAt} {
		if *value != "" {
			date, err := time.Parse(time.RFC3339Nano, *value)
			if err != nil {
				return r, fmt.Errorf("document timestamp must be RFC3339: %w", err)
			}
			*value = date.UTC().Format(time.RFC3339Nano)
		}
	}
	if r.ValidFrom != "" && r.ValidUntil != "" && !date(r.ValidUntil).After(date(r.ValidFrom)) {
		return r, fmt.Errorf("document validity interval is empty or reversed")
	}
	if r.State == "current" && (strings.TrimSpace(r.Version) == "" || r.VerifiedAt == "" || strings.TrimSpace(r.VerifiedBy) == "") {
		return r, fmt.Errorf("current document requires version, verified_at and verified_by")
	}
	if r.State == "current" && r.SupersededBy != "" {
		return r, fmt.Errorf("superseded document cannot be current")
	}
	return r, nil
}

func date(value string) time.Time { parsed, _ := time.Parse(time.RFC3339Nano, value); return parsed }

type Options struct {
	Mode    string `json:"mode,omitempty"`
	Version string `json:"version,omitempty"`
	AsOf    string `json:"as_of,omitempty"`
}

type Filter struct {
	Options
	At time.Time
}

func (o Options) Resolve(now time.Time) (Filter, error) {
	if o.Mode == "" {
		o.Mode = "current"
	}
	if o.Mode != "current" && o.Mode != "history" {
		return Filter{}, fmt.Errorf("documentation mode must be current or history")
	}
	at := now.UTC()
	if o.AsOf != "" {
		value, err := time.Parse(time.RFC3339Nano, o.AsOf)
		if err != nil {
			return Filter{}, fmt.Errorf("as_of must be RFC3339: %w", err)
		}
		at = value.UTC()
		o.AsOf = at.Format(time.RFC3339Nano)
	}
	return Filter{Options: o, At: at}, nil
}

func (f Filter) CacheKey() string {
	if f.Mode == "current" && f.Version == "" && f.AsOf == "" {
		return ""
	}
	encoded, _ := json.Marshal(f.Options)
	return "\x00document-policy:" + string(encoded)
}

func (r Record) Allows(f Filter) bool {
	if f.Version != "" && r.Version != f.Version {
		return false
	}
	if f.Mode == "current" {
		if r.State != "current" && r.State != "unverified" {
			return false
		}
		if r.SupersededBy != "" {
			return false
		}
		if r.VerifiedAt != "" && date(r.VerifiedAt).After(f.At) {
			return false
		}
	}
	if f.Mode == "current" || f.AsOf != "" {
		if r.ValidFrom != "" && date(r.ValidFrom).After(f.At) {
			return false
		}
		if r.ValidUntil != "" && !date(r.ValidUntil).After(f.At) {
			return false
		}
	}
	return true
}

func (r Record) NextChange(at time.Time) time.Time {
	var next time.Time
	for _, text := range []string{r.ValidFrom, r.ValidUntil, r.VerifiedAt} {
		if text != "" {
			value := date(text)
			if value.After(at) && (next.IsZero() || value.Before(next)) {
				next = value
			}
		}
	}
	return next
}

type filterKey struct{}

func WithFilter(ctx context.Context, f Filter) context.Context {
	return context.WithValue(ctx, filterKey{}, f)
}
func FromContext(ctx context.Context) Filter {
	if f, ok := ctx.Value(filterKey{}).(Filter); ok {
		return f
	}
	f, _ := (Options{}).Resolve(time.Now())
	return f
}

func Annotate(docMetadata map[string]any, r Record, f Filter) map[string]any {
	if docMetadata == nil {
		docMetadata = map[string]any{}
	}
	docMetadata[MetadataKey] = r
	current := f
	current.Mode = "current"
	docMetadata["_document_scope"] = map[string]any{"mode": f.Mode, "as_of": f.At.Format(time.RFC3339Nano), "eligible_for_current": r.Allows(current)}
	docMetadata["_document_policy_note"] = "Declared documentation status only; not proof of current code or deployment. Unverified documents require corroboration; planned documents do not prove implementation."
	return docMetadata
}
