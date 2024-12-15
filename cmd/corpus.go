/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/htfy96/logalign/internal"
	"github.com/pelletier/go-toml/v2"
	"github.com/phuslu/log"
	"github.com/spf13/cobra"
)

// corpusCmd represents the corpus command
var corpusCmd = &cobra.Command{
	Use:   "corpus",
	Short: "Build and maintain corpus of log calls (Check subcommands)",
	Long: `Build and maintain corpus of log calls.
The corpus is a collection of log calls from different projects. Check subcommands for more details.`,
	Run: func(cmd *cobra.Command, args []string) {
		println("Please specify a subcommand for corpus operations.")
		os.Exit(1)
	},
}

var corpusLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all corpus files",
	Long:  "List all corpus files in the specified directory",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("All Corpus Files:")
		for project, corpusFile := range internal.GlobalCorpus {
			fmt.Printf("Project: %s. File: %s\n", project, corpusFile.GetPath())
		}
	},
}

var corpusCatCmd = &cobra.Command{
	Use:   "cat {project}",
	Short: "Display the content of a corpus file",
	Long:  "Display the content of a corpus file for the specified project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		project := args[0]
		corpusFile, ok := internal.GlobalCorpus[project]
		if !ok {
			log.Fatal().Msgf("No corpus file found for project: %s\n", project)
			return
		}
		fmt.Printf("Project: %s\n", project)
		fmt.Printf("File: %s\n", corpusFile.GetPath())
		fmt.Println(corpusFile.String())
	},
}

var corpusResetAllCmd = &cobra.Command{
	Use:   "reset-all",
	Short: "Reset all corpus files to their initial state",
	Long:  "Reset all corpus files to their initial state, deleting all existing files",
	Run: func(cmd *cobra.Command, args []string) {
		err := os.RemoveAll(internal.CorpusDir)
		if err != nil {
			log.Fatal().Msgf("error removing corpus directory: %v", err)
		}
		fmt.Printf("Corpus directory %s removed\n", internal.CorpusDir)
	},
}

var corpusNewConfigCmd = &cobra.Command{
	Use:   "new-config",
	Short: "Create a new configuration file",
	Long:  "Create a new configuration file for logalign",
	Run: func(cmd *cobra.Command, args []string) {
		conf := internal.SampleLogCallDefinitionFile()
		configBytes, err := toml.Marshal(conf)
		if err != nil {
			log.Fatal().Msgf("error marshaling default config: %v", err)
		}
		err = os.WriteFile(internal.LogCallDefinitionFileName, configBytes, 0644)
		if err != nil {
			log.Fatal().Msgf("error writing default config file: %v", err)
		}
		fmt.Printf("Default configuration file created at %s\n", internal.LogCallDefinitionFileName)
	},
}

var corpusBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the corpus",
	Long:  "Build the corpus based on the current logcall definition file " + internal.LogCallDefinitionFileName,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Building Corpus...")
		repoPath := "."
		if len(args) > 0 {
			repoPath = args[0]
		}
		corpus, err := internal.BuildCorpusFromRepo(repoPath)
		if err != nil {
			log.Fatal().Msgf("error building corpus: %v", err)
			return
		}
		if err := corpus.Save(); err != nil {
			log.Fatal().Msgf("error saving corpus: %v", err)
			return
		}
		fmt.Println("Corpus built successfully")
	},
}

func init() {
	rootCmd.AddCommand(corpusCmd)
	corpusCmd.AddCommand(corpusLsCmd)
	corpusCmd.AddCommand(corpusCatCmd)
	corpusCmd.AddCommand(corpusResetAllCmd)
	corpusCmd.AddCommand(corpusNewConfigCmd)
	corpusCmd.AddCommand(corpusBuildCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// corpusCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// corpusCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
