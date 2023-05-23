// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"

	"github.com/fluxcd/go-git-providers/github"
	"github.com/fluxcd/go-git-providers/gitprovider"
)

const (
	ProviderGithub = "github"
)

// rewrite of https://github.com/fluxcd/flux2/tree/main/pkg/bootstrap/provider

var (
	// providers is a map of provider names to factory functions.
	// It is populated by calls to register.
	providers providerMap
)

func init() {
	// Register the default providers
	providers = make(providerMap)
	providers.register(ProviderGithub, githubProviderFunc)
}

// ProviderOptions contains the options for the provider
type ProviderOptions struct {
	Provider           string
	Hostname           string
	Token              string
	Username           string
	DestructiveActions bool
}

// GitProvider is a provider for git repositories
type GitProvider struct{}

// New returns a new GitProvider
func New() *GitProvider {
	return &GitProvider{}
}

// Build returns a new gitprovider.Client
func (g *GitProvider) Build(opts ProviderOptions) (gitprovider.Client, error) {
	if factory, ok := providers[opts.Provider]; ok {
		return factory(opts)
	}
	return nil, fmt.Errorf("provider %s not supported", opts.Provider)
}

// providerMap is a map of provider names to factory functions
type providerMap map[string]factoryFunc

// factoryFunc is a factory function that creates a new gitprovider.Client
type factoryFunc func(opts ProviderOptions) (gitprovider.Client, error)

// register registers a new provider
func (m providerMap) register(name string, provider factoryFunc) {
	m[name] = provider
}

// githubProviderFunc returns a new gitprovider.Client for github
func githubProviderFunc(opts ProviderOptions) (gitprovider.Client, error) {
	o := []gitprovider.ClientOption{
		gitprovider.WithOAuth2Token(opts.Token),
		gitprovider.WithDestructiveAPICalls(opts.DestructiveActions),
	}
	if opts.Hostname != "" {
		o = append(o, gitprovider.WithDomain(opts.Hostname))
	}

	client, err := github.NewClient(o...)
	if err != nil {
		return nil, err
	}
	return client, err
}
