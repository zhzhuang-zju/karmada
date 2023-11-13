package top

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/completion"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	karmadaclientset "github.com/karmada-io/karmada/pkg/generated/clientset/versioned"
	"github.com/karmada-io/karmada/pkg/karmadactl/util"
)

// TopClusterOptions contains all the options for running the top-cluster cli command.
type TopClusterOptions struct {
	Clusters           []string
	ResourceName       string
	Selector           string
	SortBy             string
	NoHeaders          bool
	UseProtocolBuffers bool
	ShowCapacity       bool

	Printer       *TopCmdPrinter
	karmadaClient karmadaclientset.Interface

	genericclioptions.IOStreams
}

var (
	topClusterLong = templates.LongDesc(i18n.T(`
                Display resource (nodes/pods/CPU/memory) usage of clusters.

                The top-cluster command allows you to see the resource consumption of clusters.`))

	topClusterExample = templates.Examples(i18n.T(`
                  # Show metrics for all clusters
                  %[1]s top clusters

                  # Show metrics for a given cluster
                  %[1]s top cluster CLUSTER_NAME`))
)

func NewCmdTopCluster(f util.Factory, parentCommand string, o *TopClusterOptions, streams genericclioptions.IOStreams) *cobra.Command {
	if o == nil {
		o = &TopClusterOptions{
			IOStreams:          streams,
			UseProtocolBuffers: true,
		}
	}

	cmd := &cobra.Command{
		Use:                   "cluster [NAME | -l label]",
		DisableFlagsInUseLine: true,
		Short:                 i18n.T("Display resource (nodes/pods/CPU/memory) usage of clusters"),
		Long:                  topClusterLong,
		Example:               fmt.Sprintf(topClusterExample, parentCommand),
		ValidArgsFunction:     completion.ResourceNameCompletionFunc(f, "cluster"),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(o.Complete(f, cmd, args))
			cmdutil.CheckErr(o.Validate())
			cmdutil.CheckErr(o.RunTopCluster(f))
		},
		Aliases: []string{"clusters", "clu"},
	}
	cmdutil.AddLabelSelectorFlagVar(cmd, &o.Selector)
	cmd.Flags().StringVar(&o.SortBy, "sort-by", o.SortBy, "If non-empty, sort nodes list using specified field. The field can be either 'node' or 'pod' or 'cpu' or 'memory'.")
	cmd.Flags().StringSliceVarP(&o.Clusters, "clusters", "C", []string{}, "-C=member1,member2")
	cmd.Flags().BoolVar(&o.NoHeaders, "no-headers", o.NoHeaders, "If present, print output without headers")
	cmd.Flags().BoolVar(&o.UseProtocolBuffers, "use-protocol-buffers", o.UseProtocolBuffers, "Enables using protocol-buffers to access Metrics API.")

	return cmd
}

func (o *TopClusterOptions) Complete(f util.Factory, cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		o.ResourceName = args[0]
	} else if len(args) > 1 {
		return cmdutil.UsageErrorf(cmd, "%s", cmd.Use)
	}

	o.Printer = NewTopCmdPrinter(o.Out)

	karmadaClient, err := f.KarmadaClientSet()
	if err != nil {
		return err
	}
	o.karmadaClient = karmadaClient

	return nil
}

func (o *TopClusterOptions) Validate() error {
	if len(o.SortBy) > 0 {
		if o.SortBy != sortByCPU && o.SortBy != sortByMemory {
			return errors.New("--sort-by accepts only cpu or memory")
		}
	}
	if len(o.ResourceName) > 0 && len(o.Selector) > 0 {
		return errors.New("only one of NAME or --selector can be provided")
	}

	if len(o.Clusters) > 0 {
		clusters, err := o.karmadaClient.ClusterV1alpha1().Clusters().List(context.TODO(), metav1.ListOptions{LabelSelector: o.Selector})
		if err != nil {
			return err
		}
		return util.VerifyWhetherClustersExist(o.Clusters, clusters)
	}

	return nil
}

func (o *TopClusterOptions) RunTopCluster(f util.Factory) error {
	var err error
	var allErrs []error

	selector := labels.Everything()
	if len(o.Selector) > 0 {
		selector, err = labels.Parse(o.Selector)
		if err != nil {
			return err
		}
	}

	clusterList := o.getTargetClusterList(selector, &allErrs)
	if len(clusterList.Items) == 0 && len(allErrs) == 0 {
		// if we had no errors, be sure we output something.
		fmt.Fprintln(o.ErrOut, "No resources found")
	}

	err = o.Printer.PrintClusterMetrics(clusterList.Items, o.NoHeaders, o.SortBy)
	if err != nil {
		allErrs = append(allErrs, err)
	}

	return utilerrors.NewAggregate(allErrs)
}

func (o *TopClusterOptions) getTargetClusterList(selector labels.Selector, allErr *[]error) *v1alpha1.ClusterList {
	clusterList := &v1alpha1.ClusterList{}
	var err error
	if len(o.Clusters) != 0 {
		for _, cluster := range o.Clusters {
			targetCluster, err := o.karmadaClient.ClusterV1alpha1().Clusters().Get(context.TODO(), cluster, metav1.GetOptions{})
			if err != nil {
				*allErr = append(*allErr, fmt.Errorf("failed to get member cluster (%s) in control plane, err: %w", cluster, err))
				return nil
			}
			clusterList.Items = append(clusterList.Items, *targetCluster)
		}
	} else {
		clusterList, err = o.karmadaClient.ClusterV1alpha1().Clusters().List(context.TODO(), metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if err != nil {
			*allErr = append(*allErr, fmt.Errorf("failed to list member clusters in control plane, err: %w", err))
			return nil
		}
	}

	return clusterList
}
