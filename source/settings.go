package source

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/whooslizi/kinkoku/abi"
)

// Setting is the subset of a settings.json item needed to register defaults.
type Setting struct {
	Type    string          `json:"type"`
	Key     string          `json:"key,omitempty"`
	Title   string          `json:"title,omitempty"`
	Default json.RawMessage `json:"default,omitempty"`
	Values  []string        `json:"values,omitempty"`
	Titles  []string        `json:"titles,omitempty"`
	Items   []Setting       `json:"items,omitempty"`
}

// PreferredLanguage picks defaults for multi-language sources (KINKOKU_LANG, default "en").
func PreferredLanguage() string {
	if l := os.Getenv("KINKOKU_LANG"); l != "" {
		return l
	}
	return "en"
}

func ReadSettings(dir string) []Setting {
	items := []Setting{}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		json.Unmarshal(b, &items)
	}
	return items
}

func LoadDefaults(dir string) (*abi.Defaults, error) {
	info, err := ReadInfo(dir)
	if err != nil {
		return nil, err
	}
	d, err := abi.LoadDefaults(filepath.Join(dir, "defaults.json"))
	if err != nil {
		return nil, err
	}
	registerDefaults(d, dir, info)
	return d, nil
}

// registerDefaults mirrors Aidoku: settings.json defaults, plus the implicit language and base
// URL settings.
func registerDefaults(d *abi.Defaults, dir string, info *Info) {
	registerSettings(d, ReadSettings(dir))

	if langs := info.Info.Languages; len(langs) > 1 {
		pick := langs[0]
		for _, l := range langs {
			if l == PreferredLanguage() {
				pick = l
			}
		}
		if info.Config.LanguageSelectType == "single" {
			d.Register("language", pick)
		} else {
			d.Register("languages", []string{pick})
		}
	}
	if info.Config.AllowsBaseURLSelect && len(info.Info.URLs) > 1 {
		d.Register("url", info.Info.URLs[0])
	}
}

func registerSettings(d *abi.Defaults, items []Setting) {
	for _, s := range items {
		var v any
		switch s.Type {
		case "group", "page":
			registerSettings(d, s.Items)
			continue
		case "select":
			var str string
			if json.Unmarshal(s.Default, &str) == nil {
				v = str
			} else if len(s.Values) > 0 {
				v = s.Values[0]
			}
		case "multi-select", "editable-list":
			var arr []string
			if json.Unmarshal(s.Default, &arr) == nil {
				v = arr
			}
		case "switch":
			var b bool
			if json.Unmarshal(s.Default, &b) == nil {
				v = b
			}
		case "stepper":
			var f float32
			if json.Unmarshal(s.Default, &f) == nil {
				v = f
			}
		case "segment":
			var n int32
			if json.Unmarshal(s.Default, &n) == nil {
				v = n
			}
		case "text":
			var str string
			if json.Unmarshal(s.Default, &str) == nil {
				v = str
			}
		}
		if v != nil && s.Key != "" {
			d.Register(s.Key, v)
		}
	}
}
