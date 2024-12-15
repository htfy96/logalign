/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/htfy96/logalign/internal"
	"github.com/phuslu/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/atomic"
)

// viewCmd represents the view command
var viewCmd = &cobra.Command{
	Use:   "view",
	Short: "View and annotate logs",
	Long:  `Output log lines based on previously built corpus`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		corpus, err := internal.ReadCorpus()
		if err != nil {
			log.Fatal().Msgf("error reading corpus: %v", err)
			return
		}
		startPos, err := cmd.PersistentFlags().GetInt("start_pos")
		if err != nil {
			log.Fatal().Msgf("error getting start_pos: %v", err)
			return
		}
		startCharPos, err := cmd.PersistentFlags().GetString("start_char_pos")
		if err != nil {
			log.Fatal().Msgf("error getting start_char_pos: %v", err)
			return
		}
		projects, err := cmd.PersistentFlags().GetStringArray("projects")
		if err != nil {
			log.Fatal().Msgf("error getting projects: %v", err)
			return
		}
		config := internal.ViewConfig{
			MinMatchChars:         viper.GetInt("min_match_chars"),
			MinMatchWordChars:     viper.GetInt("min_match_word_chars"),
			MinMatchedRatio:       viper.GetFloat64("min_matched_ratio"),
			StartPos:              startPos,
			StartCharPos:          startCharPos,
			SourceColumnWidth:     viper.GetInt("source_column_width"),
			SkipPrintArgumentExpr: viper.GetBool("skip_print_argument_expr"),
			ProjectFilter:         projects,
		}
		if err := config.Validate(); err != nil {
			log.Fatal().Msgf("error validating config: %v", err)
			return
		}
		view, err := internal.NewViewer(config, corpus)

		if err != nil {
			log.Fatal().Msgf("error creating view: %v", err)
			return
		}
		defer view.Close()

		type InputLine struct {
			Line    int
			Content string
		}

		currLine := atomic.NewInt64(0)
		inputQueue := internal.NewSafeQueue[InputLine]()

		completionQueue := internal.NewOrderPreservingCompletionQueue[string]()
		completionChan := completionQueue.GetCompletionChan()
		terminationChan := make(chan int)

		outputLine := 0

		// handlers
		for i := 0; i < 32; i++ {
			go func() {
				for {
					line := inputQueue.WaitToPop()
					processed, err := view.ProcessLine(line.Content)
					if err != nil {
						completionQueue.Push(line.Line, fmt.Sprintf("Line %d: %v", line.Line, err))
						continue
					}
					completionQueue.Push(line.Line, processed)
				}
			}()
		}

		go func() {
			reader := os.Stdin
			if len(args) > 0 {
				reader, err = os.Open(args[0])
				if err != nil {
					log.Fatal().Msgf("error opening file: %v", err)
					os.Exit(1)
				}
			}
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				line := scanner.Text()
				oldCurrLine := currLine.Add(1) - 1

				inputQueue.Push(InputLine{
					Content: line,
					Line:    int(oldCurrLine),
				})
			}
			terminationChan <- 1
		}()

		terminated := false
		for {
			select {
			case line := <-completionChan:
				println(line)
				outputLine++
			case <-terminationChan:
				terminated = true
			}
			if terminated && int(currLine.Load()) == outputLine {
				return
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(viewCmd)
	viper.SetDefault("min_match_chars", 4)
	viper.SetDefault("min_match_word_chars", 3)
	viper.SetDefault("source_column_width", 40)
	viper.SetDefault("skip_print_argument_expr", false)
	viper.SetDefault("min_matched_ratio", 0.3)
	viewCmd.PersistentFlags().Int("min_match_chars", 4, "Minimum number of non-directive characters in string formatter to match in a log line to qualify as a match")
	viper.BindPFlag("min_match_chars", viewCmd.PersistentFlags().Lookup("min_match_chars"))
	viewCmd.PersistentFlags().Int("min_match_word_chars", 3, "Minimum number of word characters in a log line to match in a log line to qualify as a match")
	viper.BindPFlag("min_match_word_chars", viewCmd.PersistentFlags().Lookup("min_match_word_chars"))
	viewCmd.PersistentFlags().Int("start_pos", 1, "Start position for matching in log lines. (1-indexed)")
	viewCmd.PersistentFlags().String("start_char_pos", "", "Only start to match log lines after n-th appearance of a specific character. "+
		"If not provided, start_pos will be used. Example usage: --start_char_pos ' 1' will match only log lines after the first space.")
	viewCmd.PersistentFlags().Int("source_column_width", 40, "Width of the source column in the output. Setting it to 0 will disable the source column.")
	viper.BindPFlag("source_column_width", viewCmd.PersistentFlags().Lookup("source_column_width"))
	viewCmd.PersistentFlags().Float64("min_matched_ratio", 0.3, "Minimum ratio of matched characters to total characters in a log line to qualify as a match")
	viper.BindPFlag("min_matched_ratio", viewCmd.PersistentFlags().Lookup("min_matched_ratio"))
	viewCmd.PersistentFlags().Bool("skip_print_argument_expr", false, "Skip printing the matched argument expr in the output")
	viper.BindPFlag("skip_print_argument_expr", viewCmd.PersistentFlags().Lookup("skip_print_argument_expr"))
	viewCmd.PersistentFlags().StringArray("projects", []string{}, "Filter logs based on project names. If not provided, all logs will be displayed")
}
