package util

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
)

func VerifyWhetherClustersExist(input []string, clusters *v1alpha1.ClusterList) error {
	clusterSet := sets.NewString()
	for _, cluster := range clusters.Items {
		clusterSet.Insert(cluster.Name)
	}

	var noneExistClusters []string
	for _, cluster := range input {
		if !clusterSet.Has(cluster) {
			noneExistClusters = append(noneExistClusters, cluster)
		}
	}
	if len(noneExistClusters) != 0 {
		return fmt.Errorf("clusters don't exist: " + strings.Join(noneExistClusters, ","))
	}

	return nil
}
