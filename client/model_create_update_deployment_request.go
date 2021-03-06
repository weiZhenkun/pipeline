/*
 * Pipeline API
 *
 * Pipeline v0.3.0 swagger
 *
 * API version: 0.3.0
 * Contact: info@banzaicloud.com
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package client

type CreateUpdateDeploymentRequest struct {
	Name string `json:"name"`
	// Version of the deployment. If not specified, the latest version is used.
	Version string `json:"version,omitempty"`
	// The chart content packaged by `helm package`. If specified chart version is ignored.
	Package     string `json:"package,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	ReleaseName string `json:"releaseName,omitempty"`
	ReuseValues bool   `json:"reuseValues,omitempty"`
	// current values of the deployment
	Values map[string]map[string]interface{} `json:"values,omitempty"`
}
