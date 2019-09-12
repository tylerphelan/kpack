package source

import (
	"errors"

	"github.com/pivotal/kpack/pkg/apis/build/v1alpha1"
)

//go:generate counterfeiter . Resolver
type Resolver interface {
	Resolve(sourceResolver *v1alpha1.SourceResolver) (v1alpha1.ResolvedSourceConfig, error)
	CanResolve(*v1alpha1.SourceResolver) bool
}

type Resolve struct {
	Resolvers []Resolver
}

func (r *Resolve) Run(source *v1alpha1.SourceResolver) (oldResolver *v1alpha1.SourceResolver, needUpdate bool, err error) {
	oldResolver = source.DeepCopy()
	var resolver Resolver
	for _, candidateResolver := range r.Resolvers {
		if candidateResolver.CanResolve(source) {
			resolver = candidateResolver
			break
		}
	}

	if resolver == nil {
		return nil, false, errors.New("invalid source type")
	}

	resolvedSource, err := resolver.Resolve(oldResolver)
	newSourceResolver := r.updateSourceResolver(oldResolver, resolvedSource)

	return newSourceResolver, newSourceResolver.CompareStatus(oldResolver), nil
}

func (r *Resolve) updateSourceResolver(sourceResolver *v1alpha1.SourceResolver, config v1alpha1.ResolvedSourceConfig) *v1alpha1.SourceResolver {
	resolvedSource := config.ResolvedSource()

	if resolvedSource.IsUnknown() && sourceResolver.Status.ObservedGeneration == sourceResolver.ObjectMeta.Generation {
		return sourceResolver
	}

	sourceResolver = sourceResolver.DeepCopy()

	sourceResolver.Status.Source = config

	sourceResolver.BecomeReady()

	sourceResolver.UpdatePollingStatus(resolvedSource.IsPollable())

	return sourceResolver
}
