package internal

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	sitterC "github.com/smacker/go-tree-sitter/c"
	sitterCpp "github.com/smacker/go-tree-sitter/cpp"
	sitterGolang "github.com/smacker/go-tree-sitter/golang"
	sitterJava "github.com/smacker/go-tree-sitter/java"
	sitterJavascript "github.com/smacker/go-tree-sitter/javascript"
	sitterPython "github.com/smacker/go-tree-sitter/python"
	sitterTypescript "github.com/smacker/go-tree-sitter/typescript/tsx"
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
	{
		Suffixes:       []string{".java"},
		Name:           "Java",
		SitterLanguage: sitterJava.GetLanguage(),
	},
	{
		Suffixes:       []string{".py"},
		Name:           "Python",
		SitterLanguage: sitterPython.GetLanguage(),
	},
	{
		Suffixes:       []string{".go"},
		Name:           "Go",
		SitterLanguage: sitterGolang.GetLanguage(),
	},
	{
		Suffixes:       []string{".js", ".mjs", ".cjs", ".jsx"},
		Name:           "Javascript",
		SitterLanguage: sitterJavascript.GetLanguage(),
	},
	{
		Suffixes:       []string{".ts", ".tsx"},
		Name:           "Typescript",
		SitterLanguage: sitterTypescript.GetLanguage(),
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
