package vcard

import (
	"bytes"
	"errors"
	"io"
	"strings"

	govcard "github.com/emersion/go-vcard"
	"github.com/google/uuid"
)

// ValidateVCard parses the input and checks basic CardDAV expectations:
// - At least one card present
// - Each card has VERSION and FN
func ValidateVCard(raw []byte) error {
	cards, err := parseAll(raw)
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		return errors.New("no vcard found")
	}
	for i, c := range cards {
		ver := c.Value(govcard.FieldVersion)
		if ver == "" {
			return errors.New("vcard missing VERSION")
		}
		fn := c.Value(govcard.FieldFormattedName)
		if fn == "" {
			return errors.New("vcard missing FN")
		}
		_ = i
	}
	return nil
}

// NormalizeVCard parses, optionally upgrades/coerces version, ensures required
// fields, and returns a canonical text/vcard with CRLF line endings and folded
// lines per encoder. If targetVersion is "", preserve existing; otherwise
// pass "3.0" or "4.0".
func NormalizeVCard(raw []byte, targetVersion string) ([]byte, error) {
	cards, err := parseAll(raw)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, errors.New("no vcard found")
	}

	for _, c := range cards {
		if c.Value(govcard.FieldFormattedName) == "" {
			return nil, errors.New("vcard missing FN")
		}

		if c.Value(govcard.FieldUID) == "" {
			c.SetValue(govcard.FieldUID, uuid.NewString())
		}

		if c.Value(govcard.FieldFormattedName) == "" {
			if name := c.Name(); name != nil {
				fn := strings.TrimSpace(strings.Join([]string{
					name.GivenName, name.AdditionalName, name.FamilyName,
				}, " "))
				if fn != "" {
					c.SetValue(govcard.FieldFormattedName, fn)
				}
			}
		}

		switch targetVersion {
		case "4.0":
			// Upgrade to v4 (in-place)
			govcard.ToV4(c)
			c.SetValue(govcard.FieldVersion, "4.0")
		case "3.0":
			// Downgrade path: go-vcard doesn't provide ToV3; we set VERSION to 3.0
			// and rely on encoder producing valid output. Note: some v4-only fields
			// may remain; most clients accept superset. For strictness, strip known
			// v4-only fields if needed.
			c.SetValue(govcard.FieldVersion, "3.0")
		case "":
			// Preserve, but if missing, default to 3.0 for broad client support
			if c.Value(govcard.FieldVersion) == "" {
				c.SetValue(govcard.FieldVersion, "3.0")
			}
		default:
			return nil, errors.New("unsupported target vcard version")
		}
	}

	// Re-encode
	var buf bytes.Buffer
	enc := govcard.NewEncoder(&buf)
	for _, c := range cards {
		if err := enc.Encode(c); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// Helper: parse all cards from a byte slice using go-vcard.
func parseAll(b []byte) ([]govcard.Card, error) {
	dec := govcard.NewDecoder(bytes.NewReader(b))
	var out []govcard.Card
	for {
		c, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
