package helm

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/portainer/libhelm"
	"github.com/portainer/libhelm/options"
	httperror "github.com/portainer/libhttp/error"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/http/middlewares"
	"github.com/portainer/portainer/api/http/security"
	"github.com/portainer/portainer/api/kubernetes"
)

const (
	handlerActivityContext = "Kubernetes"
)

type requestBouncer interface {
	AuthenticatedAccess(h http.Handler) http.Handler
}

// Handler is the HTTP handler used to handle environment(endpoint) group operations.
type Handler struct {
	*mux.Router
	requestBouncer     requestBouncer
	dataStore          portainer.DataStore
	kubeConfigService  kubernetes.KubeConfigService
	helmPackageManager libhelm.HelmPackageManager
}

// NewHandler creates a handler to manage environment(endpoint) group operations.
func NewHandler(bouncer requestBouncer, dataStore portainer.DataStore, helmPackageManager libhelm.HelmPackageManager, kubeConfigService kubernetes.KubeConfigService) *Handler {
	h := &Handler{
		Router:             mux.NewRouter(),
		requestBouncer:     bouncer,
		dataStore:          dataStore,
		helmPackageManager: helmPackageManager,
		kubeConfigService:  kubeConfigService,
	}

	h.Use(middlewares.WithEndpoint(dataStore.Endpoint(), "id"))

	// `helm list -o json`
	h.Handle("/{id}/kubernetes/helm",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.helmList))).Methods(http.MethodGet)

	// `helm delete RELEASE_NAME`
	h.Handle("/{id}/kubernetes/helm/{release}",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.helmDelete))).Methods(http.MethodDelete)

	// `helm install [NAME] [CHART] flags`
	h.Handle("/{id}/kubernetes/helm",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.helmInstall))).Methods(http.MethodPost)

	h.Handle("/{id}/kubernetes/helm/repositories",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.userGetHelmRepos))).Methods(http.MethodGet)
	h.Handle("/{id}/kubernetes/helm/repositories",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.userCreateHelmRepo))).Methods(http.MethodPost)

	return h
}

// NewTemplateHandler creates a template handler to manage environment(endpoint) group operations.
func NewTemplateHandler(bouncer requestBouncer, helmPackageManager libhelm.HelmPackageManager) *Handler {
	h := &Handler{
		Router:             mux.NewRouter(),
		helmPackageManager: helmPackageManager,
		requestBouncer:     bouncer,
	}

	h.Handle("/templates/helm",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.helmRepoSearch))).Methods(http.MethodGet)

	// helm show [COMMAND] [CHART] [REPO] flags
	h.Handle("/templates/helm/{command:chart|values|readme}",
		bouncer.AuthenticatedAccess(httperror.LoggerHandler(h.helmShow))).Methods(http.MethodGet)

	return h
}

// getHelmClusterAccess obtains the core k8s cluster access details from request.
// The cluster access includes the cluster server url, the user's bearer token and the tls certificate.
// The cluster access is passed in as kube config CLI params to helm binary.
func (handler *Handler) getHelmClusterAccess(r *http.Request) (*options.KubernetesClusterAccess, *httperror.HandlerError) {
	endpoint, err := middlewares.FetchEndpoint(r)
	if err != nil {
		return nil, &httperror.HandlerError{http.StatusNotFound, "Unable to find an environment on request context", err}
	}

	bearerToken, err := security.ExtractBearerToken(r)
	if err != nil {
		return nil, &httperror.HandlerError{http.StatusUnauthorized, "Unauthorized", err}
	}

	kubeConfigInternal := handler.kubeConfigService.GetKubeConfigInternal(endpoint.ID, bearerToken)
	return &options.KubernetesClusterAccess{
		ClusterServerURL:         kubeConfigInternal.ClusterServerURL,
		CertificateAuthorityFile: kubeConfigInternal.CertificateAuthorityFile,
		AuthToken:                kubeConfigInternal.AuthToken,
	}, nil
}
