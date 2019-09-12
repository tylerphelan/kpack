package sourceresolver

import (
	"context"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/knative/pkg/controller"

	"github.com/pivotal/kpack/pkg/apis/build/v1alpha1"
	"github.com/pivotal/kpack/pkg/client/clientset/versioned"
	v1alpha1informers "github.com/pivotal/kpack/pkg/client/informers/externalversions/build/v1alpha1"
	v1alpha1listers "github.com/pivotal/kpack/pkg/client/listers/build/v1alpha1"
	"github.com/pivotal/kpack/pkg/reconciler"
)

const (
	ReconcilerName = "SourceResolvers"
	Kind           = "SourceResolver"
)

func NewController(
	opt reconciler.Options,
	sourceResolverInformer v1alpha1informers.SourceResolverInformer,
	gitResolver Resolver,
	blobResolver Resolver,
	registryResolver Resolver,
) *controller.Impl {
	c := &Reconciler{
		GitResolver:          gitResolver,
		BlobResolver:         blobResolver,
		RegistryResolver:     registryResolver,
		Client:               opt.Client,
		SourceResolverLister: sourceResolverInformer.Lister(),
	}

	impl := controller.NewImpl(c, opt.Logger, ReconcilerName)

	c.Enqueuer = &workQueueEnqueuer{
		enqueueAfter: impl.EnqueueAfter,
		delay:        opt.SourcePollingFrequency,
	}

	sourceResolverInformer.Informer().AddEventHandler(reconciler.Handler(impl.Enqueue))

	return impl
}

//go:generate counterfeiter . Enqueuer
type Enqueuer interface {
	Enqueue(*v1alpha1.SourceResolver) error
}

type ResolveSource interface {
	Run(source *v1alpha1.SourceResolver) (oldResolver *v1alpha1.SourceResolver, needUpdate bool, err error)
}

type Reconciler struct {
	ResolveSource        ResolveSource
	Enqueuer             Enqueuer
	Client               versioned.Interface
	SourceResolverLister v1alpha1listers.SourceResolverLister
}

func (c *Reconciler) Reconcile(ctx context.Context, key string) error {
	namespace, sourceResolverName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	sourceResolver, err := c.SourceResolverLister.SourceResolvers(namespace).Get(sourceResolverName)
	if k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	newSourceResolver, needUpdate, err := c.ResolveSource.Run(sourceResolver)

	if newSourceResolver.PollingReady() {
		err := c.Enqueuer.Enqueue(sourceResolver)
		if err != nil {
			return err
		}
	}
	
	if !needUpdate {
		return nil
	}

	sourceResolver.Status.ObservedGeneration = sourceResolver.Generation
	_, err = c.Client.BuildV1alpha1().SourceResolvers(sourceResolver.Namespace).UpdateStatus(sourceResolver)
	return err
}
