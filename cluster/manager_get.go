package cluster

import (
	"context"

	"github.com/banzaicloud/pipeline/model"
	"github.com/goph/emperror"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// GetClusters returns the cluster instances for an organization ID.
func (m *Manager) GetClusters(ctx context.Context, organizationID uint) ([]CommonCluster, error) {
	logger := m.getLogger(ctx).WithFields(logrus.Fields{
		"organization": organizationID,
	})

	logger.Debug("fetching clusters from database")

	clusterModels, err := m.clusters.FindByOrganization(organizationID)
	if err != nil {
		return nil, err
	}

	var clusters []CommonCluster

	for _, clusterModel := range clusterModels {
		logger := logger.WithField("cluster", clusterModel.Name)
		logger.Debug("converting cluster model to common cluster")

		cluster, err := GetCommonClusterFromModel(clusterModel)
		if err != nil {
			logger.Error("converting cluster model to common cluster failed")

			continue
		}

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// GetAllCLusters returns all cluster instances.
func (m *Manager) GetAllClusters(ctx context.Context) ([]CommonCluster, error) {
	logger := m.getLogger(ctx)

	logger.Debug("fetching clusters from database")

	clusterModels, err := m.clusters.All()
	if err != nil {
		return nil, err
	}

	return m.getClustersFromModels(clusterModels, logger), nil
}

// GetClusterByID returns the cluster instance for an organization ID by cluster ID.
func (m *Manager) GetClusterByID(ctx context.Context, organizationID uint, clusterID uint) (CommonCluster, error) {
	logger := m.getLogger(ctx).WithFields(logrus.Fields{
		"organization": organizationID,
		"cluster":      clusterID,
	})

	logger.Debug("getting cluster from database")

	clusterModel, err := m.clusters.FindOneByID(organizationID, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "could not get cluster from database")
	}

	cluster, err := GetCommonClusterFromModel(clusterModel)
	if err != nil {
		return nil, emperror.Wrap(err, "could not get cluster from model")
	}

	return cluster, nil
}

// GetClusterByName returns the cluster instance for an organization ID by cluster name.
func (m *Manager) GetClusterByName(ctx context.Context, organizationID uint, clusterName string) (CommonCluster, error) {
	logger := m.getLogger(ctx).WithFields(logrus.Fields{
		"organization": organizationID,
		"cluster":      clusterName,
	})

	logger.Debug("getting cluster from database")

	clusterModel, err := m.clusters.FindOneByName(organizationID, clusterName)
	if err != nil {
		return nil, errors.Wrap(err, "could not get cluster from database")
	}

	cluster, err := GetCommonClusterFromModel(clusterModel)
	if err != nil {
		return nil, emperror.Wrap(err, "could not get cluster from model")
	}

	return cluster, nil
}

// GetClustersBySecretID returns the cluster instance for an organization ID by secret ID.
func (m *Manager) GetClustersBySecretID(ctx context.Context, organizationID uint, secretID string) ([]CommonCluster, error) {
	logger := m.getLogger(ctx).WithFields(logrus.Fields{
		"organization": organizationID,
		"secret":       secretID,
	})

	logger.Debug("getting cluster from database")

	clusterModels, err := m.clusters.FindBySecret(organizationID, secretID)
	if err != nil {
		return nil, errors.Wrap(err, "could not get cluster from database")
	}

	return m.getClustersFromModels(clusterModels, logger), nil
}

func (m *Manager) getClusterFromModel(clusterModel *model.ClusterModel) (CommonCluster, error) {
	return GetCommonClusterFromModel(clusterModel)
}

func (m *Manager) getClustersFromModels(clusterModels []*model.ClusterModel, logger logrus.FieldLogger) []CommonCluster {
	var clusters []CommonCluster

	for _, clusterModel := range clusterModels {
		logger := logger.WithField("cluster", clusterModel.Name)
		logger.Debug("converting cluster model to common cluster")

		cluster, err := m.getClusterFromModel(clusterModel)
		if err != nil {
			logger.Error("converting cluster model to common cluster failed")

			continue
		}

		clusters = append(clusters, cluster)
	}

	return clusters
}
