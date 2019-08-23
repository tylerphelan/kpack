package test

import (
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sclevine/spec"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"github.com/pivotal/build-service-system/pkg/apis/build/v1alpha1"
)

func TestPrivateBuilder(t *testing.T) {
	spec.Run(t, "PrivateBuilder", testPrivateBuilderSupport, spec.Sequential())
}

func testPrivateBuilderSupport(t *testing.T, when spec.G, it spec.S) {
	var cfg config
	var clients *clients

	const (
		testNamespace      = "private-test"
		dockerSecret       = "private-docker-secret"
		builderName        = "private-build-service-builder"
		serviceAccountName = "private-image-service-account"
		builderImage       = "pivotalashwin/builder:cnb"
		imagePullSecret    = "image-pull-secret"
	)

	it.Before(func() {
		cfg = loadConfig(t)

		var err error
		clients, err = newClients(t)
		require.NoError(t, err)

		err = clients.k8sClient.CoreV1().Namespaces().Delete(testNamespace, &metav1.DeleteOptions{})
		require.True(t, err == nil || errors.IsNotFound(err))
		if err == nil {
			time.Sleep(10 * time.Second)
		}

		_, err = clients.k8sClient.CoreV1().Namespaces().Create(&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		})
		require.NoError(t, err)
	})

	it.After(func() {
		for _, tag := range cfg.generatedImageNames {
			deleteImageTag(t, tag)
		}
	})

	when("an image is applied", func() {
		it("builds an initial image", func() {
			reference, err := name.ParseReference(cfg.imageTag, name.WeakValidation)
			require.NoError(t, err)

			auth, err := authn.DefaultKeychain.Resolve(reference.Context().Registry)
			require.NoError(t, err)

			basicAuth, err := auth.Authorization()
			require.NoError(t, err)

			username, password, ok := parseBasicAuth(basicAuth)
			require.True(t, ok)

			_, err = clients.k8sClient.CoreV1().Secrets(testNamespace).Create(&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: dockerSecret,
					Annotations: map[string]string{
						"build.pivotal.io/docker": reference.Context().RegistryStr(),
					},
				},
				StringData: map[string]string{
					"username": username,
					"password": password,
				},
				Type: v1.SecretTypeBasicAuth,
			})
			require.NoError(t, err)

			_, err = clients.k8sClient.CoreV1().ServiceAccounts(testNamespace).Create(&v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name: serviceAccountName,
				},
				Secrets: []v1.ObjectReference{
					{
						Name: dockerSecret,
					},
				},
			})
			require.NoError(t, err)

			_, err = clients.k8sClient.CoreV1().Secrets(testNamespace).Create(&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: imagePullSecret,
				},
				Data: nil,
				StringData: map[string]string{
					v1.DockerConfigKey: `{ "https://index.docker.io/v1/": { "auth": "" } }`,
				},
				Type: v1.SecretTypeDockercfg,
			})
			require.NoError(t, err)

			_, err = clients.client.BuildV1alpha1().Builders(testNamespace).Create(&v1alpha1.Builder{
				ObjectMeta: metav1.ObjectMeta{
					Name: builderName,
				},
				Spec: v1alpha1.BuilderSpec{
					Image: builderImage,
					ImagePullSecrets: []v1.LocalObjectReference{
						{Name: imagePullSecret},
					},
				},
			})
			require.NoError(t, err)

			cacheSize := resource.MustParse("1Gi")

			expectedResources := v1.ResourceRequirements{
				Limits: v1.ResourceList{
					v1.ResourceMemory: resource.MustParse("1G"),
				},
				Requests: v1.ResourceList{
					v1.ResourceMemory: resource.MustParse("512M"),
				},
			}

			imageConfigs := map[string]v1alpha1.SourceConfig{
				"test-git-image": {
					Git: &v1alpha1.Git{
						URL:      "https://github.com/cloudfoundry-samples/cf-sample-app-nodejs",
						Revision: "master",
					},
				},
				"test-blob-image": {
					Blob: &v1alpha1.Blob{
						URL: "https://storage.googleapis.com/build-service/sample-apps/spring-petclinic-2.1.0.BUILD-SNAPSHOT.jar",
					},
				},
				"test-registry-image": {
					Registry: &v1alpha1.Registry{
						Image: "gcr.io/cf-build-service-public/testing/beam/source@sha256:7d8aa6c87fc659d52bf42aadf23e0aaa15b1d7ed8e41383a201edabfe9d17949",
					},
				},
			}

			for imageName, imageSource := range imageConfigs {
				imageTag := cfg.newImageTag()
				_, err = clients.client.BuildV1alpha1().Images(testNamespace).Create(&v1alpha1.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: imageName,
					},
					Spec: v1alpha1.ImageSpec{
						Tag:                         imageTag,
						BuilderRef:                  builderName,
						ServiceAccount:              serviceAccountName,
						Source:                      imageSource,
						CacheSize:                   &cacheSize,
						DisableAdditionalImageNames: true,
						Build: v1alpha1.ImageBuild{
							Resources: expectedResources,
						},
					},
				})
				require.NoError(t, err)

				validateImageCreate(t, clients, imageTag, imageName, testNamespace, expectedResources)
			}
		})
	})
}
