# logalign

LogAlign is a command-line tool to annotate log lines with links to their definitons and argument expression.

![screenshot](https://github.com/htfy96/logalign/blob/master/docs/screenshot.png)

## Usage

Download the latest linux-amd64 canary build from [Release](https://github.com/htfy96/logalign/releases/tag/canary).

### Dependency

This tool depends on `libpcre2` and `libhyperscan5` to run. Install them from your system's package manager.

### Command-line Usage

First, users need to build a **corpus** from source files to extract all relevant log calls. This guide uses [openssh](https://github.com/openssh/openssh-portable) as an example.

Clone and download the source of openssh. run `logalign corpus new-config` to generate a sample configuration. Edit `.logalign.toml` to the follows:

```toml
project = 'openssh'
# source files to grep
source_regex = '.*\.c'
ignore_source_regex = 'generated\.c$'

# Could define multiple [[definitions]] under different 'id'
[[definitions]]
id = 'openssh_logs'

# This is a Treesitter query
# See https://tree-sitter.github.io/tree-sitter/using-parsers#pattern-matching-with-queries for the full syntax
# Use https://intmainreturn0.com/ts-visualizer/ to visualize the TreeSitter AST for a given code snippet
#
# It needs to capture three components:
# - @method: the log function name
# - @format_string: a printf-like format string. Should not include surrounding quotes
# - @argument_expr. Each @argument_expr should match one argument passed to the log call. Must match the number of
#   directives in format_string
query = """

(call_expression
  function: (identifier) @method
  (#match? @method \"(logit|error|debug|fatal|logdie|verbose|debug3|debug2)(_f|_r|_fr)?\")
  arguments: (argument_list
    \"(\"
    [(concatenated_string
      (string_literal
        _*
        [(string_content)
          (escape_sequence)
        ]+ @format_string
        _*
      )+
    )
      (string_literal
        _*
        [(string_content)
          (escape_sequence)
        ]+ @format_string
        _*
      )
    ]

    (
      \",\"
      (_) @argument_expr
    )*
    \")\"
  )
)"""
# language to run this query. Currently only supports c or cpp
language = 'c'
syntax = 'printflike'
# A template string to link to the source at {file} {line}
link_template = 'https://github.com/openssh/openssh-portable/blob/master/{file}#L{line}'
# Remove redundant '\n' at the end of @format_string
strip_tailing_newline = true
```

Then, run `logalign corpus build`. It should output `Corpus built successfully`.

Check the generated corpus via `logalign corpus ls` and `logalign corpus cat openssh`.

To annoate log files based on built corpus, run `logalign corpus view /var/log/auth.log`. If your terminal supports [OSC-8](https://github.com/Alhadis/OSC8-Adoption), you can control/meta/alt click the
left source panel to jump to the definitions.

## LICENSE

Apache v2
