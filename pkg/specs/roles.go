/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package specs

import (
	"slices"

	"github.com/cloudnative-pg/machinery/pkg/stringset"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/utils"
)

// RoleOptions are the information needed to create the Role object
// with the permissions used by the CNPG instance manager
type RoleOptions struct {
	// Cluster is the cluster that should be used to generate
	// the permissions
	Cluster *apiv1.Cluster

	// BackupOrigin is the backup object that is being used to restore
	// this cluster, if any.
	BackupOrigin *apiv1.Backup

	// Roles is the list of PostgreSQL roles. It is used to grant
	// the instance manager permissions to read the secrets that
	// contain the roles' password.
	Roles []apiv1.DatabaseRole

	// ActiveBackups are the names of the Backups of the cluster that are
	// not done. The instance manager can read and write only those Backups,
	// and read only the Backup the cluster bootstraps from.
	ActiveBackups []string
}

// CreateRole create a role with the permissions needed by the instance manager
func CreateRole(opts RoleOptions) rbacv1.Role {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"configmaps",
			},
			Verbs: []string{
				"get",
				"watch",
			},
			ResourceNames: getInvolvedConfigMapNames(opts.Cluster),
		},
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"secrets",
			},
			Verbs: []string{
				"get",
				"watch",
			},
			ResourceNames: getInvolvedSecretNames(opts),
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"clusters",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
			ResourceNames: []string{
				opts.Cluster.Name,
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"clusters/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"watch",
			},
			ResourceNames: []string{
				opts.Cluster.Name,
			},
		},
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"events",
			},
			Verbs: []string{
				"create",
				"patch",
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"databases",
			},
			Verbs: []string{
				"get",
				"update",
				"list",
				"watch",
			},
			ResourceNames: []string{},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"databases/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"publications",
			},
			Verbs: []string{
				"get",
				"update",
				"list",
				"watch",
			},
			ResourceNames: []string{},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"publications/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"subscriptions",
			},
			Verbs: []string{
				"get",
				"update",
				"list",
				"watch",
			},
			ResourceNames: []string{},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"subscriptions/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
			},
		},
		{
			APIGroups: []string{
				"coordination.k8s.io",
			},
			Resources: []string{
				"leases",
			},
			Verbs: []string{
				"get",
				"update",
			},
			ResourceNames: []string{
				opts.Cluster.Name,
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"failoverquorums",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
			ResourceNames: []string{
				opts.Cluster.Name,
			},
		},
		{
			APIGroups: []string{
				apiv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"failoverquorums/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"watch",
			},
			ResourceNames: []string{
				opts.Cluster.Name,
			},
		},
		{
			APIGroups: []string{
				"postgresql.cnpg.io",
			},
			Resources: []string{
				"databaseroles",
			},
			Verbs: []string{
				"get",
				"update",
				"list",
				"watch",
			},
			ResourceNames: []string{},
		},
		{
			APIGroups: []string{
				"postgresql.cnpg.io",
			},
			Resources: []string{
				"databaseroles/status",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
			},
		},
	}

	return rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opts.Cluster.Namespace,
			Name:      opts.Cluster.Name,
			Labels: map[string]string{
				utils.KubernetesAppManagedByLabelName: utils.ManagerName,
			},
		},
		Rules: slices.Concat(rules, backupRules(opts.BackupOrigin, opts.ActiveBackups)),
	}
}

func getInvolvedSecretNames(opts RoleOptions) []string {
	involvedSecretNames := []string{
		opts.Cluster.GetReplicationSecretName(),
		opts.Cluster.GetClientCASecretName(),
		opts.Cluster.GetServerCASecretName(),
		opts.Cluster.GetServerTLSSecretName(),
		opts.Cluster.GetApplicationSecretName(),
		opts.Cluster.GetSuperuserSecretName(),
		opts.Cluster.GetLDAPSecretName(),
	}

	if opts.Cluster.Spec.Monitoring != nil {
		for _, secretName := range opts.Cluster.Spec.Monitoring.CustomQueriesSecret {
			involvedSecretNames = append(involvedSecretNames, secretName.Name)
		}
	}

	if bootstrap := opts.Cluster.Spec.Bootstrap; bootstrap != nil && bootstrap.Recovery != nil &&
		bootstrap.Recovery.Backup != nil && bootstrap.Recovery.Backup.EndpointCA != nil {
		involvedSecretNames = append(involvedSecretNames, bootstrap.Recovery.Backup.EndpointCA.Name)
	}

	involvedSecretNames = append(involvedSecretNames, backupSecrets(opts.Cluster, opts.BackupOrigin)...)
	involvedSecretNames = append(involvedSecretNames, externalClusterSecrets(opts.Cluster)...)
	involvedSecretNames = append(involvedSecretNames, managedRolesSecrets(opts.Cluster)...)
	involvedSecretNames = append(involvedSecretNames, customResourceRolesSecrets(opts.Roles)...)

	return cleanupResourceList(involvedSecretNames)
}

func getInvolvedConfigMapNames(cluster *apiv1.Cluster) []string {
	involvedConfigMapNames := []string{
		cluster.Name,
	}

	if cluster.Spec.Monitoring != nil {
		// If custom queries are used, the instance manager need privileges to read those
		// entries
		for _, configMapName := range cluster.Spec.Monitoring.CustomQueriesConfigMap {
			involvedConfigMapNames = append(involvedConfigMapNames, configMapName.Name)
		}
	}

	return cleanupResourceList(involvedConfigMapNames)
}

// cleanupResourceList returns a new list with the same elements as resourceList, where
// the empty and duplicate entries have been removed
func cleanupResourceList(resourceList []string) []string {
	result := stringset.From(resourceList).ToSortedList()
	return slices.DeleteFunc(result, func(s string) bool {
		return len(s) == 0
	})
}

func externalClusterSecrets(cluster *apiv1.Cluster) []string {
	var result []string

	for _, server := range cluster.Spec.ExternalClusters {
		if server.SSLCert != nil {
			result = append(result,
				server.SSLCert.Name)
		}
		if server.SSLRootCert != nil {
			result = append(result,
				server.SSLRootCert.Name)
		}
		if server.SSLKey != nil {
			result = append(result,
				server.SSLKey.Name)
		}
		if server.Password != nil {
			result = append(result,
				server.Password.Name)
		}
		if barmanObjStore := server.BarmanObjectStore; barmanObjStore != nil {
			result = append(result, barmanObjStore.SecretNames()...)
			if barmanObjStore.EndpointCA != nil {
				result = append(result, barmanObjStore.EndpointCA.Name)
			}
		}
		if server.PgBackRest != nil {
			result = append(result, pgbackrestRepositorySecrets(&server.PgBackRest.Repository)...)
		}
	}

	return result
}

func backupSecrets(cluster *apiv1.Cluster, backupOrigin *apiv1.Backup) []string {
	var result []string

	// Secrets needed by the configured backup method (barman or pgbackrest, never both)
	if cluster.Spec.Backup != nil && cluster.Spec.Backup.IsPgBackRestConfigured() {
		result = append(result, pgbackrestSecrets(cluster)...)
	} else if cluster.Spec.Backup != nil && cluster.Spec.Backup.BarmanObjectStore != nil {
		result = append(result, cluster.Spec.Backup.BarmanObjectStore.SecretNames()...)

		// Secrets needed by Barman, if set
		if cluster.Spec.Backup.IsBarmanEndpointCASet() {
			result = append(
				result,
				cluster.Spec.Backup.BarmanObjectStore.EndpointCA.Name)
		}
	}

	if backupOrigin != nil {
		result = append(result, backupOrigin.Status.SecretNames()...)
		if backupOrigin.Status.EndpointCA != nil {
			result = append(result, backupOrigin.Status.EndpointCA.Name)
		}
	}

	return result
}

func pgbackrestSecrets(cluster *apiv1.Cluster) []string {
	if cluster.Spec.Backup == nil || !cluster.Spec.Backup.IsPgBackRestConfigured() {
		return nil
	}

	return pgbackrestRepositorySecrets(cluster.Spec.Backup.PgBackRest.Repository)
}

func pgbackrestRepositorySecrets(repository *apiv1.PgBackRestRepository) []string {
	if repository == nil {
		return nil
	}

	var result []string
	if repository.Cipher != nil {
		result = append(result, repository.Cipher.Passphrase.Name)
	}

	s3 := repository.S3
	if s3 == nil {
		return result
	}

	if s3.AccessKeyID != nil {
		result = append(result, s3.AccessKeyID.Name)
	}
	if s3.SecretAccessKey != nil {
		result = append(result, s3.SecretAccessKey.Name)
	}

	return result
}

func managedRolesSecrets(cluster *apiv1.Cluster) []string {
	if cluster.Spec.Managed == nil {
		return nil
	}
	managedRoles := cluster.Spec.Managed.Roles
	if len(managedRoles) == 0 {
		return nil
	}
	secretNames := make([]string, 0, len(managedRoles))
	for _, role := range managedRoles {
		if role.DisablePassword || role.PasswordSecret == nil {
			continue
		}
		secretName := role.PasswordSecret.Name
		if secretName != "" {
			secretNames = append(secretNames, secretName)
		}
	}

	return secretNames
}

// backupRules returns the rules that give the instance manager access to
// Backups. A rule with no ResourceNames applies to every object, so a rule is
// added only when it has names.
func backupRules(backupOrigin *apiv1.Backup, activeBackups []string) []rbacv1.PolicyRule {
	var rules []rbacv1.PolicyRule

	if backupOrigin != nil {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups:     []string{"postgresql.cnpg.io"},
			Resources:     []string{"backups"},
			Verbs:         []string{"get"},
			ResourceNames: []string{backupOrigin.Name},
		})
	}

	names := stringset.From(activeBackups).ToSortedList()
	if len(names) == 0 {
		return rules
	}

	return append(rules,
		rbacv1.PolicyRule{
			APIGroups:     []string{"postgresql.cnpg.io"},
			Resources:     []string{"backups"},
			Verbs:         []string{"get", "patch"},
			ResourceNames: names,
		},
		rbacv1.PolicyRule{
			APIGroups:     []string{"postgresql.cnpg.io"},
			Resources:     []string{"backups/status"},
			Verbs:         []string{"get", "patch", "update"},
			ResourceNames: names,
		},
	)
}

func customResourceRolesSecrets(roles []apiv1.DatabaseRole) []string {
	result := make([]string, 0, len(roles))

	for i := range roles {
		if secretName := crdRoleSecretName(&roles[i]); secretName != "" {
			result = append(result, secretName)
		}
	}

	return result
}

// crdRoleSecretName returns the Secret the instance manager must read to apply
// the password of a DatabaseRole, be it supplied through `passwordSecret` or
// generated by the operator. Without it in the RBAC rule the instance manager
// cannot even create the role, since it cannot read the password to give it.
func crdRoleSecretName(role *apiv1.DatabaseRole) string {
	if role.Spec.DisablePassword {
		return ""
	}
	return role.GetPasswordSecretName()
}
