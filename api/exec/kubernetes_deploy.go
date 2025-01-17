package exec

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"github.com/pkg/errors"
	"github.com/portainer/portainer/api/http/proxy"
	"github.com/portainer/portainer/api/http/proxy/factory"
	"github.com/portainer/portainer/api/http/proxy/factory/kubernetes"
	"github.com/portainer/portainer/api/http/security"
	"github.com/portainer/portainer/api/kubernetes/cli"

	portainer "github.com/portainer/portainer/api"
)

// KubernetesDeployer represents a service to deploy resources inside a Kubernetes environment(endpoint).
type KubernetesDeployer struct {
	binaryPath                  string
	dataStore                   portainer.DataStore
	reverseTunnelService        portainer.ReverseTunnelService
	signatureService            portainer.DigitalSignatureService
	kubernetesClientFactory     *cli.ClientFactory
	kubernetesTokenCacheManager *kubernetes.TokenCacheManager
	proxyManager                *proxy.Manager
}

// NewKubernetesDeployer initializes a new KubernetesDeployer service.
func NewKubernetesDeployer(kubernetesTokenCacheManager *kubernetes.TokenCacheManager, kubernetesClientFactory *cli.ClientFactory, datastore portainer.DataStore, reverseTunnelService portainer.ReverseTunnelService, signatureService portainer.DigitalSignatureService, proxyManager *proxy.Manager, binaryPath string) *KubernetesDeployer {
	return &KubernetesDeployer{
		binaryPath:                  binaryPath,
		dataStore:                   datastore,
		reverseTunnelService:        reverseTunnelService,
		signatureService:            signatureService,
		kubernetesClientFactory:     kubernetesClientFactory,
		kubernetesTokenCacheManager: kubernetesTokenCacheManager,
		proxyManager:                proxyManager,
	}
}

func (deployer *KubernetesDeployer) getToken(request *http.Request, endpoint *portainer.Endpoint, setLocalAdminToken bool) (string, error) {
	tokenData, err := security.RetrieveTokenData(request)
	if err != nil {
		return "", err
	}

	kubeCLI, err := deployer.kubernetesClientFactory.GetKubeClient(endpoint)
	if err != nil {
		return "", err
	}

	tokenCache := deployer.kubernetesTokenCacheManager.GetOrCreateTokenCache(int(endpoint.ID))

	tokenManager, err := kubernetes.NewTokenManager(kubeCLI, deployer.dataStore, tokenCache, setLocalAdminToken)
	if err != nil {
		return "", err
	}

	if tokenData.Role == portainer.AdministratorRole {
		return tokenManager.GetAdminServiceAccountToken(), nil
	}

	token, err := tokenManager.GetUserServiceAccountToken(int(tokenData.ID), endpoint.ID)
	if err != nil {
		return "", err
	}

	if token == "" {
		return "", fmt.Errorf("can not get a valid user service account token")
	}
	return token, nil
}

// Deploy will deploy a Kubernetes manifest inside a specific namespace in a Kubernetes environment(endpoint).
// Otherwise it will use kubectl to deploy the manifest.
func (deployer *KubernetesDeployer) Deploy(request *http.Request, endpoint *portainer.Endpoint, stackConfig string, namespace string) (string, error) {
	command := path.Join(deployer.binaryPath, "kubectl")
	if runtime.GOOS == "windows" {
		command = path.Join(deployer.binaryPath, "kubectl.exe")
	}

	args := make([]string, 0)

	if endpoint.Type == portainer.AgentOnKubernetesEnvironment || endpoint.Type == portainer.EdgeAgentOnKubernetesEnvironment {
		url, proxy, err := deployer.getAgentURL(endpoint)
		if err != nil {
			return "", errors.WithMessage(err, "failed generating endpoint URL")
		}

		defer proxy.Close()
		args = append(args, "--server", url)
		args = append(args, "--insecure-skip-tls-verify")
	}

	token, err := deployer.getToken(request, endpoint, endpoint.Type == portainer.KubernetesLocalEnvironment)
	if err != nil {
		return "", err
	}

	args = append(args, "--token", token)
	args = append(args, "--namespace", namespace)
	args = append(args, "apply", "-f", "-")

	var stderr bytes.Buffer
	cmd := exec.Command(command, args...)
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(stackConfig)

	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrapf(err, "failed to execute kubectl command: %q", stderr.String())
	}

	return string(output), nil
}

// ConvertCompose leverages the kompose binary to deploy a compose compliant manifest.
func (deployer *KubernetesDeployer) ConvertCompose(data []byte) ([]byte, error) {
	command := path.Join(deployer.binaryPath, "kompose")
	if runtime.GOOS == "windows" {
		command = path.Join(deployer.binaryPath, "kompose.exe")
	}

	args := make([]string, 0)
	args = append(args, "convert", "-f", "-", "--stdout")

	var stderr bytes.Buffer
	cmd := exec.Command(command, args...)
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(data)

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New(stderr.String())
	}

	return output, nil
}

func (deployer *KubernetesDeployer) getAgentURL(endpoint *portainer.Endpoint) (string, *factory.ProxyServer, error) {
	proxy, err := deployer.proxyManager.CreateAgentProxyServer(endpoint)
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("http://127.0.0.1:%d/kubernetes", proxy.Port), proxy, nil
}
