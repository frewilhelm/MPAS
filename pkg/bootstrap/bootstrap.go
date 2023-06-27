// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxcd/go-git-providers/gitprovider"
	"github.com/open-component-model/mpas/pkg/printer"
	"github.com/open-component-model/ocm/pkg/contexts/ocm"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errReconciledWithWarning = errors.New("reconciled with warning")
)

// options contains the options to be used during bootstrap
type options struct {
	description      string
	defaultBranch    string
	visibility       string
	personal         bool
	owner            string
	token            string
	repositoryName   string
	target           string
	fromFile         string
	registry         string
	dockerConfigPath string
	transportType    string
	kubeclient       client.Client
	restClientGetter genericclioptions.RESTClientGetter
	components       []string
	interval         time.Duration
	timeout          time.Duration
	printer          *printer.Printer
}

// Option is a function that sets an option on the bootstrap
type Option func(*options)

// Bootstrap runs the bootstrap of mpas.
// This means it creates a new management repository and the installs the bootstrap component
// in the cluster targeted by the kubeconfig.
type Bootstrap struct {
	ProviderClient gitprovider.Client
	repository     gitprovider.UserRepository
	url            string
	options
}

// WithInterval sets the interval to use for the bootstrap component
func WithInterval(interval time.Duration) Option {
	return func(o *options) {
		o.interval = interval
	}
}

// WithTimeout sets the timeout to use for the bootstrap component
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.timeout = timeout
	}
}

// WithRESTClientGetter sets the RESTClientGetter to use for the bootstrap component
func WithRESTClientGetter(restClientGetter genericclioptions.RESTClientGetter) Option {
	return func(o *options) {
		o.restClientGetter = restClientGetter
	}
}

// WithKubeClient sets the kubeclient to use for the bootstrap component
func WithKubeClient(kubeclient client.Client) Option {
	return func(o *options) {
		o.kubeclient = kubeclient
	}
}

func WithDockerConfigPath(dockerConfigPath string) Option {
	return func(o *options) {
		o.dockerConfigPath = dockerConfigPath
	}
}

// WithTarget sets the target of the bootstrap component
func WithTarget(target string) Option {
	return func(o *options) {
		target = strings.TrimSuffix(target, "/")
		o.target = target
	}
}

// WithPrinter sets the printer to use for printing messages
func WithPrinter(printer *printer.Printer) Option {
	return func(o *options) {
		o.printer = printer
	}
}

// WithComponents sets the components to include in the management repository
func WithComponents(components []string) Option {
	return func(o *options) {
		o.components = components
	}
}

// WithDescription sets the description of the management repository
func WithDescription(description string) Option {
	return func(o *options) {
		o.description = description
	}
}

// WithDefaultBranch sets the default branch of the management repository
func WithDefaultBranch(defaultBranch string) Option {
	return func(o *options) {
		o.defaultBranch = defaultBranch
	}
}

// WithVisibility sets the visibility of the management repository
func WithVisibility(visibility string) Option {
	return func(o *options) {
		o.visibility = visibility
	}
}

// WithPersonal sets the personal flag of the management repository
func WithPersonal(personal bool) Option {
	return func(o *options) {
		o.personal = personal
	}
}

// WithOwner sets the owner of the management repository
func WithOwner(owner string) Option {
	return func(o *options) {
		o.owner = owner
	}
}

// WithToken sets the token of the management repository
func WithToken(token string) Option {
	return func(o *options) {
		o.token = token
	}
}

// WithRepositoryName sets the repository name of the management repository
func WithRepositoryName(repositoryName string) Option {
	return func(o *options) {
		o.repositoryName = repositoryName
	}
}

// WithFromFile sets the file from which to read the bootstrap component
func WithFromFile(fromFile string) Option {
	return func(o *options) {
		o.fromFile = fromFile
	}
}

// WithRegistry sets the registry to use for the bootstrap component
func WithRegistry(registry string) Option {
	return func(o *options) {
		o.registry = registry
	}
}

// WithTransportType sets the transport type to use for git operations
func WithTransportType(transportType string) Option {
	return func(o *options) {
		o.transportType = transportType
	}
}

// New returns a new Bootstrap. It accepts a gitprovider.Client and a list of options.
func New(ProviderClient gitprovider.Client, opts ...Option) *Bootstrap {
	b := &Bootstrap{
		ProviderClient: ProviderClient,
	}

	for _, opt := range opts {
		opt(&b.options)
	}

	setDefaults(b)

	return b
}

// Run runs the bootstrap of mpas and returns an error if it fails.
func (b *Bootstrap) Run(ctx context.Context) error {
	if b.fromFile != "" {
		return fmt.Errorf("bootstrap from file is not supported yet")
	}

	b.printer.Printf("Running %s ...\n",
		printer.BoldBlue("mpas bootstrap"))

	if err := b.reconcileManagementRepository(ctx); err != nil {
		return err
	}

	octx := ocm.DefaultContext()
	ociRepo, err := makeOCIRepository(octx, b.registry, b.dockerConfigPath)
	if err != nil {
		return err
	}

	refs, err := b.fetchBootstrapComponentReferences(ociRepo)
	if err != nil {
		return fmt.Errorf("failed to fetch bootstrap component references: %w", err)
	}

	for comp, ref := range refs {
		b.printer.Printf("Installing %s with version %s\n",
			printer.BoldBlue(comp),
			printer.BoldBlue(ref.GetVersion()))

		switch comp {
		case "flux":
			dir, err := mkdirTempDir("flux-install")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			inst, err := NewFluxInstall(ref.GetComponentName(), ref.GetVersion(), b.owner, ociRepo,
				withBranch(b.defaultBranch),
				withTarget(b.target),
				withKubeClient(b.kubeclient),
				withKubeConfig(b.restClientGetter),
				withURL(b.url),
				withNamespace("flux-system"),
				withDir(dir),
				withInterval(b.interval),
				withTimeout(b.timeout),
				withToken(b.token),
			)
			if err != nil {
				return err
			}
			if err := inst.Install(ctx, comp); err != nil {
				return err
			}
		case "ocm-controller":
			dir, err := mkdirTempDir("ocm-controller-install")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			inst, err := NewComponentInstall(ref.GetComponentName(), ref.GetVersion(), ociRepo,
				withComponentBranch(b.defaultBranch),
				withComponentTarget(b.target),
				withComponentKubeClient(b.kubeclient),
				withComponentNamespace("ocm-system"),
				withComponentDir(dir),
			)
			if err != nil {
				return err
			}
			if err := inst.Install(ctx, "ocm-contoller-file"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown component %q", comp)
		}
	}

	b.printer.Printf("Bootstrap completed successfully\n")

	return nil
}

// reconcileManagementRepository reconciles the management repository. It creates it if it does not exist.
func (b *Bootstrap) reconcileManagementRepository(ctx context.Context) error {
	repo, err := b.reconcileRepository(ctx, b.personal)
	if err != nil && !errors.Is(err, errReconciledWithWarning) {
		return err
	}

	b.printer.Printf("Preparing Management repository %s with branch %s and visibility %s\n",
		printer.BoldBlue(b.repositoryName),
		printer.BoldBlue(b.defaultBranch),
		printer.BoldBlue(b.visibility))

	cloneURL, err := b.getCloneURL(repo, gitprovider.TransportType(b.transportType))
	if err != nil {
		return err
	}

	b.repository = repo
	b.url = cloneURL

	return nil
}

func (b *Bootstrap) DeleteManagementRepository(ctx context.Context) error {
	if b.repository == nil {
		return fmt.Errorf("management repository is not set")
	}

	err := b.repository.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete management repository: %w", err)
	}
	return nil
}

func (b *Bootstrap) reconcileRepository(ctx context.Context, personal bool) (gitprovider.UserRepository, error) {
	var (
		repo gitprovider.UserRepository
		err  error
	)
	subOrgs, repoName := splitSubOrganizationsFromRepositoryName(b.repositoryName)
	if personal {
		userRef := newUserRef(b.ProviderClient.SupportedDomain(), b.owner)
		repoRef := newUserRepositoryRef(userRef, repoName)
		repoInfo := newRepositoryInfo(b.description, b.defaultBranch, b.visibility)
		repo, err = b.ProviderClient.UserRepositories().Get(ctx, repoRef)
		if err != nil {
			if !errors.Is(err, gitprovider.ErrNotFound) {
				return nil, fmt.Errorf("failed to get Git repository %q: %w", repoRef.String(), err)
			}
			repo, _, err = b.ProviderClient.UserRepositories().Reconcile(ctx, repoRef, repoInfo)
			if err != nil {
				return nil, fmt.Errorf("failed to reconcile Git repository %q: %w", repoRef.String(), err)
			}
		}
	} else {
		orgRef, err := b.getOrganization(ctx, subOrgs)
		if err != nil {
			return nil, fmt.Errorf("failed to reconcile Git repository %q: %w", b.repositoryName, err)
		}
		repoRef := newOrgRepositoryRef(*orgRef, repoName)
		repoInfo := newRepositoryInfo(b.description, b.defaultBranch, b.visibility)
		repo, err = b.ProviderClient.OrgRepositories().Get(ctx, repoRef)
		if err != nil {
			if !errors.Is(err, gitprovider.ErrNotFound) {
				return nil, fmt.Errorf("failed to get Git repository %q: %w", repoRef.String(), err)
			}
			repo, _, err = b.ProviderClient.OrgRepositories().Reconcile(ctx, repoRef, repoInfo)
			if err != nil {
				return nil, fmt.Errorf("failed to create new Git repository %q: %w", repoRef.String(), err)
			}
		}
	}

	return repo, nil
}

func (b *Bootstrap) getOrganization(ctx context.Context, subOrgs []string) (*gitprovider.OrganizationRef, error) {
	return &gitprovider.OrganizationRef{
		Domain:           b.ProviderClient.SupportedDomain(),
		Organization:     b.owner,
		SubOrganizations: subOrgs,
	}, nil
}

func (b *Bootstrap) getCloneURL(repository gitprovider.UserRepository, transport gitprovider.TransportType) (string, error) {
	var url string
	if cloner, ok := repository.(gitprovider.CloneableURL); ok {
		url = cloner.GetCloneURL("", transport)
	} else {
		url = repository.Repository().GetCloneURL(transport)
	}

	var err error
	if transport == gitprovider.TransportTypeSSH {
		return "", fmt.Errorf("SSH transport is not supported")
	}
	return url, err
}

func splitSubOrganizationsFromRepositoryName(name string) ([]string, string) {
	elements := strings.Split(name, "/")
	switch i := len(elements); i {
	case 1:
		return nil, name
	default:
		return elements[:i-1], elements[i-1]
	}
}

func newOrgRepositoryRef(organizationRef gitprovider.OrganizationRef, name string) gitprovider.OrgRepositoryRef {
	return gitprovider.OrgRepositoryRef{
		OrganizationRef: organizationRef,
		RepositoryName:  name,
	}
}

func newUserRef(domain, login string) gitprovider.UserRef {
	return gitprovider.UserRef{
		Domain:    domain,
		UserLogin: login,
	}
}

func newUserRepositoryRef(userRef gitprovider.UserRef, name string) gitprovider.UserRepositoryRef {
	return gitprovider.UserRepositoryRef{
		UserRef:        userRef,
		RepositoryName: name,
	}
}

func newRepositoryInfo(description, defaultBranch, visibility string) gitprovider.RepositoryInfo {
	var i gitprovider.RepositoryInfo
	if description != "" {
		i.Description = gitprovider.StringVar(description)
	}
	if defaultBranch != "" {
		i.DefaultBranch = gitprovider.StringVar(defaultBranch)
	}
	if visibility != "" {
		i.Visibility = gitprovider.RepositoryVisibilityVar(gitprovider.RepositoryVisibility(visibility))
	}
	return i
}

func setDefaults(b *Bootstrap) {
	if b.description == "" {
		b.description = "Management repository for the Open Component Model"
	}

	if b.defaultBranch == "" {
		b.defaultBranch = "main"
	}

	if b.visibility == "" {
		b.visibility = "private"
	}

	if b.transportType == "" {
		b.transportType = "https"
	}
}

func mkdirTempDir(pattern string) (string, error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", err
	}

	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return dir, nil
}
