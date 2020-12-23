package main

var (
	usageTemplate = `Usage:
{{- if .Runnable}} {{.UseLine}}{{end}}
{{- if .HasAvailableSubCommands}} {{.CommandPath}} [OPTIONS] COMMAND{{end}}

{{if ne .Long ""}}{{ .Long | trim }}{{ else }}{{ .Short | trim }}{{end}}

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
{{- if .HasParent}}

Options:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{- else}}
{{- range flagsets}}
{{if eq .Name ""}}
Options:
{{- else}}
Options for {{.Name}}:
{{- end}}
{{.Flags.FlagUsages | trimTrailingWhitespaces}}
{{- end}}
{{- end}}

{{- end}}

{{- if .HasAvailableInheritedFlags}}
{{- range flagsets}}
{{if eq .Name ""}}
Global Options:
{{- else}}
Global Options for {{.Name}}:
{{- end}}
{{.Flags.FlagUsages | trimTrailingWhitespaces}}
{{- end}}

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

	helpTemplate = `{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`
)
