/*
Copyright 2024 The Karmada Authors.

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

package top

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/completion"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	karmadaclientset "github.com/karmada-io/karmada/pkg/generated/clientset/versioned"
	"github.com/karmada-io/karmada/pkg/karmadactl/util"
)

// ClusterOptions contains all the options for running the top-cluster cli command.
type ClusterOptions struct {
	Clusters           []string
	ResourceName       string
	LabelSelector      string
	FieldSelector      string
	SortBy             string
	NoHeaders          bool
	UseProtocolBuffers bool
	ShowCapacity       bool

	Printer       *CmdPrinter
	karmadaClient karmadaclientset.Interface

	genericiooptions.IOStreams
}

var (
	topClusterLong = templates.LongDesc(i18n.T(`
                Display resource (pods/CPU/memory) usage of clusters.

                The top-cluster command allows you to see the resource consumption of clusters.`))

	topClusterExample = templates.Examples(i18n.T(`
                  # Show metrics for all clusters
                  %[1]s top clusters

                  # Show metrics for a given cluster
                  %[1]s top cluster CLUSTER_NAME`))
)

// NewCmdTopCluster implements the top cluster command.
func NewCmdTopCluster(f util.Factory, parentCommand string, o *ClusterOptions, streams genericiooptions.IOStreams) *cobra.Command {
	if o == nil {
		o = &ClusterOptions{
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
			cmdutil.CheckErr(o.RunTopCluster())
		},
	}
	cmdutil.AddLabelSelectorFlagVar(cmd, &o.LabelSelector)
	cmd.Flags().StringVar(&o.SortBy, "sort-by", o.SortBy, "If non-empty, sort nodes list using specified field. The field can be either 'pod' or 'cpu' or 'memory'.")
	cmd.Flags().BoolVar(&o.NoHeaders, "no-headers", o.NoHeaders, "If present, print output without headers")
	cmd.Flags().BoolVar(&o.UseProtocolBuffers, "use-protocol-buffers", o.UseProtocolBuffers, "Enables using protocol-buffers to access Metrics API.")
	cmd.Flags().StringVar(&o.FieldSelector, "field-selector", o.FieldSelector, "Selector (field query) to filter on, supports '=', '==', and '!='.(e.g. --field-selector key1=value1,key2=value2). The server only supports a limited number of field queries per type.")

	return cmd
}

// Complete completes all the required options.
func (o *ClusterOptions) Complete(f util.Factory, cmd *cobra.Command, args []string) error {
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

// Validate checks the validity of the options.
func (o *ClusterOptions) Validate() error {
	if len(o.SortBy) > 0 {
		if o.SortBy != sortByCPU && o.SortBy != sortByMemory && o.SortBy != sortByPod {
			return errors.New("--sort-by accepts only cpu, memory or pod")
		}
	}
	if len(o.ResourceName) > 0 && (len(o.LabelSelector) > 0 || len(o.FieldSelector) > 0) {
		return errors.New("only one of NAME or selector can be provided")
	}

	return nil
}

// RunTopCluster runs the top cluster command.
func (o *ClusterOptions) RunTopCluster() error {
	var err error
	var allErrs []error

	labelSelector := labels.Everything()
	if len(o.LabelSelector) > 0 {
		labelSelector, err = labels.Parse(o.LabelSelector)
		if err != nil {
			return err
		}
	}
	fieldSelector := fields.Everything()
	if len(o.FieldSelector) > 0 {
		fieldSelector, err = fields.ParseSelector(o.FieldSelector)
		if err != nil {
			return err
		}
	}

	clusterList := o.getTargetClusterList(labelSelector, fieldSelector, &allErrs)
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

func (o *ClusterOptions) getTargetClusterList(labelSelector labels.Selector, fieldSelector fields.Selector, allErr *[]error) *v1alpha1.ClusterList {
	clusterList := &v1alpha1.ClusterList{}
	var err error
	if len(o.ResourceName) != 0 {
		targetCluster, err := o.karmadaClient.ClusterV1alpha1().Clusters().Get(context.TODO(), o.ResourceName, metav1.GetOptions{})
		if err != nil {
			*allErr = append(*allErr, fmt.Errorf("failed to get member cluster (%s) in control plane, err: %w", o.ResourceName, err))
			return nil
		}
		clusterList.Items = append(clusterList.Items, *targetCluster)
	} else {
		clusterList, err = o.karmadaClient.ClusterV1alpha1().Clusters().List(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector.String(), FieldSelector: fieldSelector.String(),
		})
		if err != nil {
			*allErr = append(*allErr, fmt.Errorf("failed to list member clusters in control plane, err: %w", err))
			return nil
		}
	}

	return clusterList
}
