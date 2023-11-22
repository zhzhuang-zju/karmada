package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/tools/clientcmd"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"
)

// CurrentContextOptions holds the command-line options for 'config current-context' sub command
type CurrentContextOptions struct {
	ConfigAccess clientcmd.ConfigAccess
	streams      genericclioptions.IOStreams
}

var (
	currentContextLong = templates.LongDesc(i18n.T(`
		Display the current-context.`))

	currentContextExample = templates.Examples(`
		# Display the current-context
		%[1]s config current-context`)
)

// NewCmdConfigCurrentContext returns a Command instance for 'config current-context' sub command
func NewCmdConfigCurrentContext(parentCommand string, streams genericclioptions.IOStreams, configAccess clientcmd.ConfigAccess) *cobra.Command {
	options := &CurrentContextOptions{
		ConfigAccess: configAccess,
		streams:      streams,
	}

	cmd := &cobra.Command{
		Use:     "current-context",
		Short:   i18n.T("Display the current-context"),
		Long:    currentContextLong,
		Example: fmt.Sprintf(currentContextExample, parentCommand),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(options.run())
		},
	}

	return cmd
}

func (o *CurrentContextOptions) run() error {
	config, err := o.ConfigAccess.GetStartingConfig()
	if err != nil {
		return err
	}

	if config.CurrentContext == "" {
		err = fmt.Errorf("current-context is not set")
		return err
	}

	fmt.Fprintf(o.streams.Out, "%s\n", config.CurrentContext)
	return nil
}
