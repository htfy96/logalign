package internal

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	sitterC "github.com/smacker/go-tree-sitter/c"
	sitterCpp "github.com/smacker/go-tree-sitter/cpp"
)

type LanguageDef struct {
	Suffixes       []string
	Name           string
	SitterLanguage *sitter.Language
}

var LanguageDefs = []LanguageDef{
	{
		Suffixes:       []string{".c"},
		Name:           "C",
		SitterLanguage: sitterC.GetLanguage(),
	},
	{
		Suffixes:       []string{".cpp", ".cc", ".cxx", ".h", ".hpp"},
		Name:           "Cpp",
		SitterLanguage: sitterCpp.GetLanguage(),
	},
}

func GetLanguageDefByFileName(fileName string) *LanguageDef {
	for _, def := range LanguageDefs {
		for _, suffix := range def.Suffixes {
			if strings.HasSuffix(strings.ToLower(fileName), suffix) {
				return &def
			}
		}
	}
	return nil
}

func GetLanguageDefByName(name string) *LanguageDef {
	for _, def := range LanguageDefs {
		if strings.EqualFold(def.Name, name) {
			return &def
		}
	}
	return nil
}
