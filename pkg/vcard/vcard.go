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

func NormalizeVCard(raw []byte, targetVersion string) ([]byte, error) {
	cards, err := parseAll(raw)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, errors.New("no vcard found")
	}

	for _, c := range cards {
		// Set version first, before other processing
		switch targetVersion {
		case "4.0":
			c.SetValue(govcard.FieldVersion, "4.0")
			govcard.ToV4(c)
		case "3.0":
			c.SetValue(govcard.FieldVersion, "3.0")
			// Add v4-only field removal logic if needed
		case "":
			if c.Value(govcard.FieldVersion) == "" {
				c.SetValue(govcard.FieldVersion, "3.0")
			}
		default:
			return nil, errors.New("unsupported target vcard version")
		}

		// Generate FN if missing
		if c.Value(govcard.FieldFormattedName) == "" {
			if name := c.Name(); name != nil {
				fn := strings.TrimSpace(strings.Join([]string{
					name.GivenName, name.AdditionalName, name.FamilyName,
				}, " "))
				if fn != "" {
					c.SetValue(govcard.FieldFormattedName, fn)
				}
			}
			// If still no FN, this is an error per RFC
			if c.Value(govcard.FieldFormattedName) == "" {
				return nil, errors.New("vcard missing FN and cannot generate from N")
			}
		}

		// Add UID if missing
		if c.Value(govcard.FieldUID) == "" {
			c.SetValue(govcard.FieldUID, uuid.NewString())
		}
	}

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
