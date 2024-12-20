package internal

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"

	hs "github.com/flier/gohs/hyperscan"
	pcre2 "github.com/htfy96/go-pcre2/v2"
	"github.com/muesli/termenv"
	"github.com/phuslu/log"
	"github.com/spf13/viper"
)

type ViewConfig struct {
	MinMatchChars     int
	MinMatchWordChars int
	MinMatchedRatio   float64
	// 1-indexed startPos of match for a log line
	StartPos int
	// A single character followed by a position index (1-indexed) to start matching log lines after a specific character
	StartCharPos          string
	SourceColumnWidth     int
	SkipPrintArgumentExpr bool
	ProjectFilter         []string
}

func (vc ViewConfig) MustGetStartCharPos() (byte, int) {
	idx, err := strconv.Atoi(vc.StartCharPos[1:])
	if err != nil {
		log.Panic().Msgf("start_char_pos: invalid character '%c': %s", vc.StartCharPos[0], err)
	}
	return vc.StartCharPos[0], idx
}

func (vc ViewConfig) Validate() error {
	if vc.MinMatchChars < 0 {
		return fmt.Errorf("min_match_chars must be non-negative")
	}
	if vc.StartPos < 0 {
		return fmt.Errorf("start_pos must be a non-negative, 1-based integer")
	}
	if vc.SourceColumnWidth < 0 {
		return fmt.Errorf("source_column_width must be non-negative")
	}
	if len(vc.StartCharPos) > 0 && vc.StartPos > 1 {
		return fmt.Errorf("cannot use both start_pos and start_char_pos together")
	}
	if len(vc.StartCharPos) > 0 {
		if len(vc.StartCharPos) < 2 {
			return fmt.Errorf("start_char_pos must be at least a two-character string like {character}{posIdx}")
		}
		if idx, err := strconv.Atoi(vc.StartCharPos[1:]); err != nil {
			return fmt.Errorf("start_char_pos: invalid posIdx '%s': %w", vc.StartCharPos[1:], err)
		} else if idx < 1 {
			return fmt.Errorf("start_char_pos: posIdx must be a positive integer")
		}
	}

	return nil
}

type LogCallRef struct {
	Project   string
	CallIndex int
}

type Viewer struct {
	Config ViewConfig
	Corpus Corpus
	// Project ==> list[len(calls.Calls)] Regex
	CompiledRegex                    map[LogCallRef]*pcre2.Regexp
	CompiledAllRegex                 hs.BlockDatabase
	CompiledAllPatternIDToLogCallMap map[int]LogCallRef
	DefinitionIDToDefinitionMap      map[string]*LogCallDefinition
}

func getRegexGroupName(lcRef LogCallRef) string {
	return fmt.Sprintf("%s__%d__", lcRef.Project, lcRef.CallIndex)
}

func (v *Viewer) getLogCallFromRef(lcRef LogCallRef) *LogCall {
	calls := v.Corpus[lcRef.Project].Calls
	return &calls[lcRef.CallIndex]
}

func buildOrLoadCachedHSPatternsDB(patterns []*hs.Pattern) (hs.BlockDatabase, error) {
	cacheDir := viper.GetString("cache_dir")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}
	hash := fnv.New64()
	hash.Write([]byte("HSPATV1"))
	for _, pattern := range patterns {
		hash.Write([]byte(pattern.Expression))
	}
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%x.hsdb", hash.Sum64()))
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		db, err := hs.NewBlockDatabase(patterns...)
		if err != nil {
			return nil, fmt.Errorf("failed to create HS block database: %w", err)
		}
		serialized, err := db.Marshal()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal HS block database: %w", err)
		}
		err = os.WriteFile(cachePath, serialized, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write HS block database cache at %s: %w", cachePath, err)
		}
		log.Info().Msgf("Created HS block database cache at %s", cachePath)
		return db, nil
	} else {
		serialized, err := os.ReadFile(cachePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read HS block database cache %s: %w", cachePath, err)
		}
		db, err := hs.UnmarshalBlockDatabase(serialized)
		if err != nil {
			return nil, fmt.Errorf("failed to load HS block database from cache %s: %w", cachePath, err)
		}
		return db, nil
	}
}

func NewViewer(config ViewConfig, corpus Corpus) (*Viewer, error) {
	compiledRegex := make(map[LogCallRef]*pcre2.Regexp, 0)

	hsPatterns := make([]*hs.Pattern, 0)
	compiledAllPatternIDToLogCallMap := make(map[int]LogCallRef)
	definitionIDToDefinitionMap := make(map[string]*LogCallDefinition)

	for project, calls := range corpus {
		if len(config.ProjectFilter) > 0 && !slices.Contains(config.ProjectFilter, project) {
			continue
		}
		definitionsMap := make(map[string]*LogCallDefinition, 0)
		for _, def := range calls.Definitions {
			definitionsMap[def.ID] = &def
		}
		for i, call := range calls.Calls {
			def := definitionsMap[call.DefinitionID]
			if def.Syntax == LogCallSyntaxPrintflike {
				parsed, err := ParsePrintfFormat(call.FormatString, getRegexGroupName(LogCallRef{
					Project: project, CallIndex: i}))
				if err != nil {
					return nil, fmt.Errorf("failed to parse printf-like format string %q from %s.%d : %s", call.FormatString, project, i, err)
				}
				compiled, err := pcre2.CompileJIT(parsed.Regex+"$", 0, pcre2.JIT_COMPLETE)
				if err != nil {
					return nil, fmt.Errorf("failed to compile regex for %s: %s", parsed.Regex, err)
				}
				compiledRegex[LogCallRef{Project: project, CallIndex: i}] = compiled

				hsPat := hs.NewPattern(parsed.HyperScanRegex+"$", 0)
				if hsPat == nil {
					return nil, fmt.Errorf("failed to create hyperscan pattern: %s", parsed.HyperScanRegex)
				}
				info, err := hsPat.Info()
				if err != nil {
					return nil, fmt.Errorf("failed to get hyperscan pattern info: %s", err)
				}
				if info.MinWidth == 0 {
					log.Info().Msgf("Ignoring hyperscan pattern with zero width: %s from %s:%d", parsed.HyperScanRegex, call.File, call.Line)
					continue
				}
				hsPatterns = append(hsPatterns, hsPat)
				hsPat.Id = len(compiledAllPatternIDToLogCallMap) + 1
				_, exists := compiledAllPatternIDToLogCallMap[hsPat.Id]
				if exists {
					return nil, fmt.Errorf("duplicate hyperscan pattern ID: %d", hsPat.Id)
				}
				compiledAllPatternIDToLogCallMap[hsPat.Id] = LogCallRef{
					Project:   project,
					CallIndex: i,
				}

			} else {
				return nil, fmt.Errorf("unsupported log call syntax: %s", def.Syntax)
			}
		}
		for _, def := range calls.Definitions {
			if _, ok := definitionIDToDefinitionMap[def.ID]; ok {
				return nil, fmt.Errorf("duplicate definition ID: %s", def.ID)
			}
			definitionIDToDefinitionMap[def.ID] = &def
		}
	}

	db, err := buildOrLoadCachedHSPatternsDB(hsPatterns)
	if err != nil {
		return nil, fmt.Errorf("failed to create hyperscan block database: %s", err)
	}
	return &Viewer{
		Config:                           config,
		Corpus:                           corpus,
		CompiledRegex:                    compiledRegex,
		CompiledAllRegex:                 db,
		CompiledAllPatternIDToLogCallMap: compiledAllPatternIDToLogCallMap,
		DefinitionIDToDefinitionMap:      definitionIDToDefinitionMap,
	}, nil
}

func (v *Viewer) Close() {
	for _, compiledRegex := range v.CompiledRegex {
		compiledRegex.Free()
	}
	v.CompiledAllRegex.Close()
}

const refColumnSeparator = " | "

func (v *Viewer) buildRefColumn(file string, line int, link string) string {
	output := termenv.NewOutput(os.Stdout)
	if v.Config.SourceColumnWidth == 0 {
		return ""
	}

	res := strings.Builder{}

	if file == "" && line == 0 {
		for i := 0; i < v.Config.SourceColumnWidth-len(refColumnSeparator); i++ {
			res.WriteByte(' ')
		}
		res.WriteString(refColumnSeparator)
		return res.String()
	}
	localRes := strings.Builder{}
	localRes.WriteString(file)
	localRes.WriteString(":")
	localRes.WriteString(strconv.Itoa(line))
	if localRes.Len() > v.Config.SourceColumnWidth-len(refColumnSeparator) {
		styledString := output.String(localRes.String()[:v.Config.SourceColumnWidth-len(refColumnSeparator)-3] + "...").Foreground(output.Color("#dddddd")).String()
		res.WriteString(styledString)
	} else {
		res.WriteString(output.String(localRes.String()).Foreground(output.Color("#dddddd")).String())
		for i := 0; i < v.Config.SourceColumnWidth-len(refColumnSeparator)-localRes.Len(); i++ {
			res.WriteByte(' ')
		}
	}
	res.WriteString(refColumnSeparator)
	return termenv.Hyperlink(link, res.String())
}

func (v *Viewer) AllocScratch() (*hs.Scratch, error) {
	return hs.NewScratch(v.CompiledAllRegex)
}

func (v *Viewer) ProcessLine(line string, scratch *hs.Scratch) (string, error) {
	startPos := 0
	if v.Config.StartPos > 1 {
		startPos = v.Config.StartPos - 1
	} else if len(v.Config.StartCharPos) > 0 {
		char, cnt := v.Config.MustGetStartCharPos()
		for cnt > 0 {
			newPos := strings.IndexByte(line[startPos:], char)
			if newPos == -1 {
				startPos = len(line) - 1
				break
			}
			startPos += newPos + 1
			cnt--
		}
	}
	lineToMatch := line[min(startPos, len(line)):]
	prefix := line[:min(startPos, len(line))]

	processedMatched := lineToMatch
	refFile := ""
	refLine := 0
	refLink := ""

	type Match struct {
		LcRef    LogCallRef
		From, To uint64
	}
	type MatchKey struct {
		Id   int
		From uint64
	}
	matches := make(map[MatchKey]Match)
	handler := hs.MatchHandler(func(id uint, from, to uint64, flags uint, context interface{}) error {
		log.Trace().Msgf("Got hyperscan match from %d: %d-%d. LcRef: %v", id, from, to, v.CompiledAllPatternIDToLogCallMap[int(id)])
		if to-from < uint64(v.Config.MinMatchChars) || to-from < uint64(v.Config.MinMatchedRatio*float64(len(lineToMatch))) {
			return nil
		}
		if oldMatch, exists := matches[MatchKey{Id: int(id), From: from}]; exists {
			if oldMatch.To > to {
				return nil
			}
		}
		matches[MatchKey{
			Id:   int(id),
			From: from,
		}] = Match{
			LcRef: v.CompiledAllPatternIDToLogCallMap[int(id)],
			From:  from,
			To:    to,
		}
		return nil
	})
	if err := v.CompiledAllRegex.Scan([]byte(lineToMatch), scratch, handler, nil); err != nil {
		log.Warn().Msgf("hyperscan scan failed: %s", err)
	} else {

		bestMatchedLiterals := 0
		bestMatchedTotal := 0
		bestMatchedWordLiterals := 0
		bestMatched := MatchKey{}
		for key, match := range matches {
			regex := v.CompiledRegex[match.LcRef]
			matcher := regex.MatcherString(lineToMatch, 0)
			defer matcher.Free()

			logCall := v.getLogCallFromRef(match.LcRef)
			if !matcher.Matches() {
				log.Info().Msgf("Hyperscan reported match for log call %s.%d (%s) on %s, but no match was found with %s", match.LcRef.Project, match.LcRef.CallIndex,
					logCall.FormatString,
					regex.Pattern,
					lineToMatch)
				continue
			}
			totalMatched := matcher.Index()[1] - matcher.Index()[0]
			totalMatchedLiterals := totalMatched
			totalMatchedWordLiterals := 0
			for i := matcher.Index()[0]; i < matcher.Index()[1]; i++ {
				if syntax.IsWordChar(rune(lineToMatch[i])) {
					totalMatchedWordLiterals++
				}
			}
			log.Trace().Msgf("For %s: Total matched characters: %d", regex.Pattern, totalMatched)
			for i := 0; i < 1000; i++ {
				argName := fmt.Sprintf("arg%s%d", getRegexGroupName(match.LcRef), i)
				if argRange, err := matcher.Named(argName); err == nil {
					totalMatchedLiterals -= len(argRange)
					for _, b := range argRange {
						if syntax.IsWordChar(rune(b)) {
							totalMatchedWordLiterals--
						}
					}
				} else {
					break
				}
			}

			// Compare and update (bestMatchedWordLiterals, bestMatchedLiterals, bestMatchedTotal)
			// with the current match
			if totalMatchedWordLiterals > bestMatchedWordLiterals ||
				(totalMatchedWordLiterals == bestMatchedWordLiterals && totalMatchedLiterals > bestMatchedLiterals) ||
				(totalMatchedWordLiterals == bestMatchedWordLiterals && totalMatchedLiterals == bestMatchedLiterals && totalMatched > bestMatchedTotal) {
				bestMatched = key
				bestMatchedWordLiterals = totalMatchedWordLiterals
				bestMatchedLiterals = totalMatchedLiterals
				bestMatchedTotal = totalMatched
			}

		}
		if bestMatchedTotal == 0 {
			if len(matches) > 0 {
				log.Warn().Msgf("No pcre2 match found for line despite that Hyperscan think so: %s", lineToMatch)
			}
		} else {
			bestMatchedRecord := matches[bestMatched]
			logCall := v.getLogCallFromRef(bestMatchedRecord.LcRef)
			if bestMatchedLiterals >= v.Config.MinMatchChars && bestMatchedWordLiterals >= v.Config.MinMatchWordChars && float64(bestMatchedTotal) >= v.Config.MinMatchedRatio*float64(len(lineToMatch)) {
				output := termenv.NewOutput(os.Stdout)
				// This line is a match!
				refFile = logCall.File
				refLine = logCall.Line
				definition := v.DefinitionIDToDefinitionMap[logCall.DefinitionID]
				refLink = strings.ReplaceAll(definition.LinkTemplate, "{file}", refFile)
				refLink = strings.ReplaceAll(refLink, "{line}", strconv.Itoa(refLine))
				if !v.Config.SkipPrintArgumentExpr {
					processedMatchedBuilder := strings.Builder{}
					regex := v.CompiledRegex[bestMatchedRecord.LcRef]
					// Very ugly hack, matcher.Named() only returns a byteSlice and didn't
					// contain the start and end indices of the match. We have to recover it
					// using byte slice cap
					lineToMatchBytes := []byte(lineToMatch)
					matcher := regex.Matcher(lineToMatchBytes, 0)
					defer matcher.Free()
					prevEnd := matcher.Index()[0]
					processedMatchedBuilder.WriteString(lineToMatch[:prevEnd])
					for i := 0; i < 1000; i++ {
						argName := fmt.Sprintf("arg%s%d", getRegexGroupName(bestMatchedRecord.LcRef), i)
						if argRange, err := matcher.Named(argName); err == nil {
							argStartPos := cap(lineToMatchBytes) - cap(argRange)
							argEndPos := argStartPos + len(argRange)
							if argStartPos < 0 || argStartPos < prevEnd || argEndPos >= len(lineToMatch)+1 {
								log.Panic().Msgf("Invalid PCRE2 match range: %v. Cap(range): %d. Cap(lineToMatch): %d", argRange, cap(argRange), cap(lineToMatchBytes))
							}
							processedMatchedBuilder.WriteString(lineToMatch[prevEnd:argStartPos])
							argExpr := strings.ReplaceAll(logCall.ArgumentExprs[i], "\n", "\\n")
							processedMatchedBuilder.WriteString(output.String("|" + argExpr + "|").Foreground(output.Color("#006633")).Background(output.Color("#202020")).String())
							processedMatchedBuilder.WriteString(lineToMatch[argStartPos:argEndPos])
							prevEnd = argEndPos
						} else {
							break
						}
					}
					processedMatchedBuilder.WriteString(lineToMatch[prevEnd:])
					processedMatched = processedMatchedBuilder.String()
				}
			}
		}
	}

	refColumn := v.buildRefColumn(refFile, refLine, refLink)

	return fmt.Sprintf("%s%s%s", refColumn, prefix, processedMatched), nil
}
