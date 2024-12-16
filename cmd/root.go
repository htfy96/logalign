/*
Copyright © 2024 Zheng 'Vic' Luo vicluo96@gmail.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/htfy96/logalign/internal"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/adrg/xdg"
	"github.com/phuslu/log"
)

var cfgFile string

func initFromGlobalConfig() {

	log.DefaultLogger.Level = log.ParseLevel(viper.GetString("loglevel"))
	log.DefaultLogger = log.Logger{
		Level:      log.ParseLevel(viper.GetString("loglevel")),
		Caller:     1,
		TimeField:  "time",
		TimeFormat: "2006-01-02 15:04:05",
		Writer: &log.ConsoleWriter{
			ColorOutput: true,
		},
	}
	internal.CorpusDir = viper.GetString("corpus_dir")
	if _, err := os.Stat(internal.CorpusDir); os.IsNotExist(err) {
		log.Info().Msgf("Creating corpus directory at %s", internal.CorpusDir)
	}
	// create the directory if it doesn't exist
	err := os.MkdirAll(internal.CorpusDir, 0755)
	if err != nil {
		log.Fatal().Msgf("error creating data directory: %v", err)
	}

	internal.GlobalCorpus, err = internal.ReadCorpus()
	if err != nil {
		log.Fatal().Msgf("error reading corpus: %v", err)
	}

	if cpuProfile, err := rootCmd.PersistentFlags().GetString("cpuprofile"); err != nil {
		log.Fatal().Msgf("error parsing cpuprofile flag: %v", err)
	} else if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			log.Fatal().Msgf("error creating cpu profile file: %s", err)
		}
		pprof.StartCPUProfile(f)
	}

}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "logalign {corpus | view} [flags...]",
	Short: "Annotate logs with links to their definitions and arguments",
	Long: `A command-line tool to extract log definitions from sources, and annotate log lines with links to their definitions and arguments.

'logalign corpus' builds and maintains a corpus of log calls from different projects.
'logalign view' outputs log lines based on previously built corpus.

Some flags (e.g., corpus_dir, cache_dir, loglevel, source_column_width, min_matched_ratio, skip_print_argument_expr) can be set via $XDG_CONFIG_HOME/.logalign.yaml or ~/.logalign.yaml.

Set 'CLICOLOR_FORCE' or 'NO_COLOR' to force color output regardless of the terminal.
`,

	Run: func(cmd *cobra.Command, args []string) {
		println("Please specify a subcommand for logalign operations.")
		os.Exit(1)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.logalign.yaml)")
	rootCmd.PersistentFlags().String("corpus_dir", "", "corpus directory (default is $XDG_STATE_HOME/logalign)")
	viper.BindPFlag("corpus_dir", rootCmd.PersistentFlags().Lookup("corpus_dir"))
	rootCmd.PersistentFlags().String("cache_dir", "", "cache directory (default is $XDG_CACHE_HOME/logalign)")
	viper.BindPFlag("cache_dir", rootCmd.PersistentFlags().Lookup("cache_dir"))
	rootCmd.PersistentFlags().String("loglevel", "info", "log level (trace, debug, info, warn, error, fatal, panic)")
	viper.BindPFlag("loglevel", rootCmd.PersistentFlags().Lookup("loglevel"))

	rootCmd.PersistentFlags().String("cpuprofile", "", "write cpu profile to file")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {

	viper.SetDefault("corpus_dir", xdg.StateHome+"/logalign")
	viper.SetDefault("cache_dir", xdg.CacheHome+"/logalign")
	viper.SetDefault("loglevel", "warn")
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".logalign" (without extension).
		viper.AddConfigPath(home)
		viper.AddConfigPath(xdg.ConfigHome + "/logalign")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".logalign")
	}
	viper.SetEnvPrefix("LOGALIGN")

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}

	initFromGlobalConfig()
}
