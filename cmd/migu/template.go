package main

var (
	usageTemplate = `Usage:
{{- if .Runnable}} {{.UseLine}}{{end}}
{{- if .HasAvailableSubCommands}} {{.CommandPath}} [OPTIONS] COMMAND{{end}}

{{- if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}

{{- end}}

{{- if .HasExample}}

Examples:
{{.Example}}

{{- end}}

{{- if .HasAvailableSubCommands}}

Commands:

{{- range .Commands}}
{{- if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- end}}
{{- end}}

{{- end}}

{{- if .HasAvailableLocalFlags}}

Options:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

{{- end}}

{{- if .HasAvailableInheritedFlags}}

Global Options:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}

{{- end}}

{{- if .HasHelpSubCommands}}

Additional help topics:

{{- range .Commands}}
{{- if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}
{{- end}}
{{- end}}

{{- end}}

{{- if .HasAvailableSubCommands}}

Run '{{.CommandPath}} COMMAND --help' for more information about a command.
{{end}}
`
)
