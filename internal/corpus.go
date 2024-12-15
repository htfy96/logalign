package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/phuslu/log"
	"github.com/schollz/progressbar/v3"
	sitter "github.com/smacker/go-tree-sitter"
)

type LogCallSyntax string

const (
	LogCallSyntaxPrintflike LogCallSyntax = "printflike"
)

const CorpusFilePrefix = "corpus_project_"

var LogCallDefinitionFileName = ".logalign.toml"

// CorpusDir is the directory where the corpus files are stored.
// Must be set before using any corpus-related functions.
var CorpusDir string

type LogCallDefinition struct {
	ID                  string            `json:"id" toml:"id"`
	Query               string            `json:"query" toml:"query,multiline"`
	Language            string            `json:"language" toml:"language"`
	Syntax              LogCallSyntax     `json:"syntax" toml:"syntax"`
	LinkTemplate        string            `json:"link_template" toml:"link_template"`
	StripTailingNewLine bool              `json:"strip_tailing_newline" toml:"strip_tailing_newline,omitempty"`
	CustomAttrs         map[string]string `json:"custom_attrs,omitempty" toml:"custom_attrs,omitempty"`
	// Only populated in LogCallDefinitionFile
	CompiledQuery *sitter.Query `json:"-" toml:"omitempty"`
}

func (def *LogCallDefinition) Close() {
	if def.CompiledQuery != nil {
		def.CompiledQuery.Close()
	}
}

func (def *LogCallDefinition) Compile() error {
	langDef := GetLanguageDefByName(strings.ToLower(def.Language))
	if langDef == nil {
		return fmt.Errorf("language not found: %s", def.Language)
	}
	query, err := sitter.NewQuery([]byte(def.Query), langDef.SitterLanguage)
	if err != nil {
		return fmt.Errorf("invalid %s query in %v: %s", def.Language, def, err)
	}
	def.CompiledQuery = query
	return nil

}

type LogCall struct {
	Project       string   `json:"project"`
	File          string   `json:"file"`
	Line          int      `json:"line"`
	DefinitionID  string   `json:"definition_id"`
	Method        string   `json:"method"`
	FormatString  string   `json:"format_string"`
	ArgumentExprs []string `json:"argument_exprs"`
}

type LogCallDefinitionFile struct {
	Project           string              `toml:"project"`
	SourceRegex       string              `toml:"source_regex,omitempty"`
	IgnoreSourceRegex string              `toml:"ignore_source_regex,omitempty"`
	Definitions       []LogCallDefinition `toml:"definitions"`
}

func SampleLogCallDefinitionFile() LogCallDefinitionFile {
	return LogCallDefinitionFile{
		Project:           "linux",
		SourceRegex:       "drivers/net/.*\\.c",
		IgnoreSourceRegex: "generated\\.c$",
		Definitions: []LogCallDefinition{
			{
				ID: "printk",
				Query: `
(call_expression
  function: (identifier) @method
  (#eq? @method "printk")
  arguments: (argument_list
    "("
    (concatenated_string
      (identifier) @loglevel
      (string_literal
        _*
        [(string_content)
          (escape_sequence)
        ]+ @format_string
        _*
      )+
    )
    (
      ","
      (_) @argument_expr
    )* 
    ")"
  )
)`,
				Language:            "c",
				Syntax:              LogCallSyntaxPrintflike,
				LinkTemplate:        "https://sourcegraph.com/github.com/torvalds/linux/-/blob/{file}?L{line}",
				StripTailingNewLine: true,
			},
		},
	}
}

func (c *LogCallDefinitionFile) Close() {
	for _, def := range c.Definitions {
		def.Close()
	}
}

type CorpusFile struct {
	Project     string              `json:"project"`
	Definitions []LogCallDefinition `json:"definitions,omitempty"`
	Calls       []LogCall           `json:"calls,omitempty"`
}

func (c *CorpusFile) String() string {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Panic().Msgf("error marshalling corpus file: %s", err)
	}
	return string(data)
}

func (c *CorpusFile) GetPath() string {
	return filepath.Join(CorpusDir, fmt.Sprintf("%s%s.json", CorpusFilePrefix, c.Project))
}
func (c *CorpusFile) Save() error {
	log.Info().Msgf("Saving corpus file for project %s", c.Project)
	filePath := c.GetPath()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshalling corpus file: %w", err)
	}
	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing corpus file: %w", err)
	}
	return nil
}

// Corpus is a map of project names to their corresponding corpus files.
type Corpus map[string]CorpusFile

func NewCorpus() Corpus {
	return make(Corpus)
}

func (c Corpus) AddCorpusFile(file *CorpusFile) {
	c[file.Project] = *file
}

var GlobalCorpus Corpus

func ReadCorpus() (Corpus, error) {

	if CorpusDir == "" {
		return nil, fmt.Errorf("corpus directory not set")
	}
	log.Info().Msgf("Reading corpus from %s", CorpusDir)
	corpus := NewCorpus()
	files, err := os.ReadDir(CorpusDir)
	if err != nil {
		return nil, fmt.Errorf("error reading corpus directory: %w", err)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), CorpusFilePrefix) {
			continue
		}
		filePath := filepath.Join(CorpusDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("error reading corpus file %q: %w", filePath, err)
		}
		var corpusFile CorpusFile
		err = json.Unmarshal(data, &corpusFile)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling corpus file %q: %w", filePath, err)
		}
		corpus.AddCorpusFile(&corpusFile)
	}
	return corpus, nil
}

func collectSourceFiles(repoRoot string, sourceRegex string, ignoreSourceRegex string) ([]string, error) {
	sourceRegexCompiled, err := regexp.Compile(sourceRegex)
	if err != nil {
		return nil, fmt.Errorf("error compiling source regex: %w", err)
	}
	ignoreSourceRegexCompiled, err := regexp.Compile(ignoreSourceRegex)
	if err != nil {
		return nil, fmt.Errorf("error compiling ignore source regex: %w", err)
	}
	filterFile := func(filePath string) bool {
		if ignoreSourceRegex != "" && ignoreSourceRegexCompiled.MatchString(filePath) {
			return false
		}
		return sourceRegex == "" || sourceRegexCompiled.MatchString(filePath)
	}
	sourceFiles := []string{}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err == nil {
		log.Debug().Msgf("Building corpus from Git repository at %s", repoRoot)
		cmd := exec.Command("git", "-C", repoRoot, "ls-files", "--full-name")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("error running git command: %w", err)
		}
		sourceFiles = strings.Split(string(out), "\n")
	} else {
		log.Debug().Msgf("Building corpus from local directory at %s", repoRoot)
		filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Warn().Msgf("error walking directory %s: %s", path, err)
				return err
			}
			if !info.IsDir() {
				path, _ = strings.CutPrefix(path, repoRoot)
				path = strings.TrimPrefix(path, "/")
				sourceFiles = append(sourceFiles, path)
			}
			return nil
		})
	}
	filteredSourceFiles := []string{}
	for _, filePath := range sourceFiles {
		if filterFile(filePath) {
			filteredSourceFiles = append(filteredSourceFiles, filePath)
		} else {
			log.Trace().Msgf("Ignoring file %s", filePath)
		}
	}
	log.Debug().Msgf("Collected %d source files", len(filteredSourceFiles))
	return filteredSourceFiles, nil
}

func extractLogCalls(repoRoot string, filePath string, project string, definitions []LogCallDefinition) ([]LogCall, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	fullPath := filepath.Join(repoRoot, filePath)
	matchedDefinitions := make([]*LogCallDefinition, 0)
	log.Trace().Msgf("Processing file %s", fullPath)
	logCalls := []LogCall{}

	langDef := GetLanguageDefByFileName(filePath)
	if langDef == nil {
		log.Info().Msgf("Language definition for file %s not found", filePath)
		return logCalls, nil
	}

	parser.SetLanguage(langDef.SitterLanguage)
	source, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("error reading file %q: %w", fullPath, err)
	}
	tree, err := parser.ParseCtx(context.Background(), nil, source)
	if err != nil {
		return nil, fmt.Errorf("error parsing file %q: %w", fullPath, err)
	}
	for _, definition := range definitions {
		if strings.EqualFold(definition.Language, langDef.Name) {
			matchedDefinitions = append(matchedDefinitions, &definition)
		}
	}
	for _, matchedDef := range matchedDefinitions {
		cursor := sitter.NewQueryCursor()
		defer cursor.Close()
		cursor.Exec(matchedDef.CompiledQuery, tree.RootNode())
		for match, ok := cursor.NextMatch(); ok; match, ok = cursor.NextMatch() {
			match = cursor.FilterPredicates(match, source)
			if len(match.Captures) == 0 {
				continue
			}

			method := ""
			formatString := ""
			argumentExprs := []string{}
			mainCapture := match.Captures[0]
			for _, capture := range match.Captures {
				log.Trace().Msgf("Query %s Captured capture %d (name %s): %s", matchedDef.Query, capture.Index,
					matchedDef.CompiledQuery.CaptureNameForId(capture.Index),
					capture.Node.Content(source))
				if matchedDef.CompiledQuery.CaptureNameForId(capture.Index) == "method" {
					method = capture.Node.Content(source)
				} else if matchedDef.CompiledQuery.CaptureNameForId(capture.Index) == "format_string" {
					formatString += capture.Node.Content(source)
				} else if matchedDef.CompiledQuery.CaptureNameForId(capture.Index) == "argument_expr" {
					argumentExprs = append(argumentExprs, capture.Node.Content(source))
				}
			}
			if method == "" {
				log.Warn().Msgf("Failed to extract method from log call from match %s at file %s", mainCapture.Node.Content(source), fullPath)
				continue
			}
			if formatString == "" {
				log.Warn().Msgf("Failed to extract format string from log call from match %s at file %s", mainCapture.Node.Content(source), fullPath)
				continue
			}
			if matchedDef.StripTailingNewLine {
				formatString = strings.TrimSuffix(formatString, "\n")
				formatString = strings.TrimSuffix(formatString, "\\n")
			}
			logCalls = append(logCalls, LogCall{
				Project:       project,
				File:          filePath,
				Line:          int(mainCapture.Node.StartPoint().Row) + 1,
				Method:        method,
				FormatString:  formatString,
				ArgumentExprs: argumentExprs,
				DefinitionID:  matchedDef.ID,
			})
			log.Trace().Msgf("Found log call in match %s at file %s: %+v", mainCapture.Node.Content(source), fullPath, logCalls[len(logCalls)-1])
		}
	}

	validatedLogCalls := make([]LogCall, 0)
	// Validate logCalls
	definitionsMap := make(map[string]*LogCallDefinition, 0)
	for _, definition := range definitions {
		definitionsMap[definition.ID] = &definition
	}
	for _, logCall := range logCalls {
		matchedDef := definitionsMap[logCall.DefinitionID]
		if matchedDef.Syntax == LogCallSyntaxPrintflike {
			parsed, err := ParsePrintfFormat(logCall.FormatString, "test")
			if err != nil {
				log.Info().Msgf("Failed to parse printf-like format string %q from %s:%d : %s", logCall.FormatString, logCall.File, logCall.Line, err)
				continue
			}
			if parsed.ArgCnt != len(logCall.ArgumentExprs) {
				log.Info().Msgf("Argument count mismatch in log call %v: expected %d, got %d", logCall, parsed.ArgCnt, len(logCall.ArgumentExprs))
				continue
			}
			validatedLogCalls = append(validatedLogCalls, logCall)
		}
	}
	return validatedLogCalls, nil
}

func BuildCorpusFromRepo(repoRoot string) (CorpusFile, error) {
	logCallDefinitionFile := LogCallDefinitionFile{
		SourceRegex:       "",
		IgnoreSourceRegex: "",
		Definitions:       make([]LogCallDefinition, 0),
	}
	logCallDefinitionFilePath := filepath.Join(repoRoot, LogCallDefinitionFileName)
	if _, err := os.Stat(logCallDefinitionFilePath); err != nil {
		return CorpusFile{}, fmt.Errorf("error reading logcall definition file: %w", err)
	}
	data, err := os.ReadFile(logCallDefinitionFilePath)
	if err != nil {
		return CorpusFile{}, fmt.Errorf("error reading logcall definition file: %w", err)
	}
	if err := toml.Unmarshal(data, &logCallDefinitionFile); err != nil {
		return CorpusFile{}, fmt.Errorf("error unmarshalling logcall definition file: %w", err)
	}
	for i := range logCallDefinitionFile.Definitions {
		if err = logCallDefinitionFile.Definitions[i].Compile(); err != nil {
			return CorpusFile{}, fmt.Errorf("invalid log call definition: %w", err)
		}
	}
	defer logCallDefinitionFile.Close()
	files, err := collectSourceFiles(repoRoot, logCallDefinitionFile.SourceRegex, logCallDefinitionFile.IgnoreSourceRegex)
	if err != nil {
		return CorpusFile{}, fmt.Errorf("error collecting source files: %w", err)
	}
	pbar := progressbar.Default(int64(len(files)))
	completeChan := make(chan []LogCall)
	for _, file := range files {
		go func(filePath string) {
			logCalls, err := extractLogCalls(repoRoot, filePath, logCallDefinitionFile.Project, logCallDefinitionFile.Definitions)
			pbar.Add(1)
			if err != nil {
				log.Error().Msgf("Error extracting log calls from file %s: %v", filePath, err)
				completeChan <- []LogCall{}
			} else {
				completeChan <- logCalls
			}
		}(file)
	}
	corpusFile := CorpusFile{
		Project:     logCallDefinitionFile.Project,
		Definitions: logCallDefinitionFile.Definitions,
		Calls:       []LogCall{},
	}
	completedCnt := 0
	for completedCnt < len(files) {
		logCalls := <-completeChan
		corpusFile.Calls = append(corpusFile.Calls, logCalls...)
		completedCnt++
	}
	return corpusFile, nil
}
