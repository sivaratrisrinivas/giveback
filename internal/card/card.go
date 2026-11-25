// Package card reads a wedding or function invitation and pulls out who is
// inviting, so their names can be looked up in the family notebook.
package card

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// Person is someone named on the card.
type Person struct {
	NameEnglish string `json:"name_english"` // as printed in English, or transliterated
	NameTelugu  string `json:"name_telugu"`  // as printed in Telugu script, "" if absent
	Surname     string `json:"surname"`      // family name if identifiable, e.g. "Butham"
	Role        string `json:"role"`         // see prompt: host, groom, bride, bride_parent, ...
	Deceased    bool   `json:"deceased"`     // "Late", "కీ.శే."
	Place       string `json:"place"`        // town or village tied to this person, if any
}

// Invitation is what Giveback needs from a card.
type Invitation struct {
	FunctionType string   `json:"function_type"` // wedding, housewarming, ...
	Date         string   `json:"date"`          // YYYY-MM-DD if printed
	Venue        string   `json:"venue"`
	Town         string   `json:"town"`
	People       []Person `json:"people"`
	Languages    []string `json:"languages"`
}

// Hosts returns the people who are inviting: the ones to look up in the notebook.
func (inv Invitation) Hosts() []Person {
	var hs []Person
	for _, p := range inv.People {
		if p.Role == "host" {
			hs = append(hs, p)
		}
	}
	return hs
}

const prompt = `You are reading an Indian (usually Telugu) wedding or function invitation card.
The card may have an English side and a Telugu side with the same content; merge them, do not list anyone twice.

List every person named on the card in "people", with a role:
- "host": the people inviting the guest: the family whose son or daughter is marrying, who sign
  the invitation. On Telugu cards they usually appear near the bottom, after "మీ ఆగమనాభిలాషులు"
  ("awaiting your arrival"), often as "Sri X - Smt Y" with a house address and phone number.
- "groom", "bride": the couple ("Chi." and "Chi.La.Sow.").
- "bride_parent" or "groom_parent": the OTHER side's parents mentioned in brackets, e.g. "D/o Sri A - Smt B".
  They are not the hosts.
- "well_wisher": names in "With best compliments from" / "అభినందనలతో" lines.
- "other": anyone else.

For each person give name_english (as printed, or transliterate from Telugu), name_telugu (exact Telugu script if printed),
surname if you can tell it (in Telugu names the surname usually comes FIRST, e.g. "Butham Muthyalu" -> surname "Butham"),
deceased=true if marked "Late" or "కీ.శే.", and place if a town or village is tied to them.
Drop titles like Sri, Smt, Chi., garu from the names.

Also give function_type, date as YYYY-MM-DD, venue, town, and languages present.
Return only JSON with keys: function_type, date, venue, town, languages, people.
Do not guess: if something is not printed, use an empty string.`

var schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"function_type": map[string]any{"type": "string"},
		"date":          map[string]any{"type": "string"},
		"venue":         map[string]any{"type": "string"},
		"town":          map[string]any{"type": "string"},
		"languages":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"people": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name_english": map[string]any{"type": "string"},
				"name_telugu":  map[string]any{"type": "string"},
				"surname":      map[string]any{"type": "string"},
				"role": map[string]any{"type": "string", "enum": []string{
					"host", "groom", "bride", "bride_parent", "groom_parent", "well_wisher", "other"}},
				"deceased": map[string]any{"type": "boolean"},
				"place":    map[string]any{"type": "string"},
			},
			"required": []string{"name_english", "name_telugu", "surname", "role", "deceased", "place"},
		}},
	},
	"required": []string{"function_type", "date", "venue", "town", "languages", "people"},
}

// Read extracts the invitation from one or more card images (front, back, inside).
func Read(ctx context.Context, r vision.Asker, imgs ...vision.Image) (Invitation, vision.Result, error) {
	res, err := r.ReadJSON(ctx, prompt, schema, imgs...)
	if err != nil {
		return Invitation{}, res, err
	}
	var inv Invitation
	if err := json.Unmarshal(res.JSON, &inv); err != nil {
		return Invitation{}, res, fmt.Errorf("card JSON from %s: %w", res.Model, err)
	}
	return inv, res, nil
}
