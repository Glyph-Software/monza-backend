package image

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// PullAndFlatten downloads an OCI image and writes a single flattened tar of
// all layers to disk. Returns the path to the tar file and the image digest.
func PullAndFlatten(ctx context.Context, imageRef string, cacheDir string) (tarPath string, digest string, err error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", "", fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}

	log.Printf("image puller: pulling %s", ref.String())

	desc, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return "", "", fmt.Errorf("remote get %q: %w", imageRef, err)
	}

	img, err := desc.Image()
	if err != nil {
		return "", "", fmt.Errorf("resolve image %q: %w", imageRef, err)
	}

	d, err := img.Digest()
	if err != nil {
		return "", "", fmt.Errorf("image digest: %w", err)
	}
	digest = d.Hex[:12]

	flatDir := filepath.Join(cacheDir, "flat")
	if err := os.MkdirAll(flatDir, 0o755); err != nil {
		return "", "", err
	}

	tarPath = filepath.Join(flatDir, digest+".tar")
	if _, err := os.Stat(tarPath); err == nil {
		log.Printf("image puller: using cached tar %s", tarPath)
		return tarPath, digest, nil
	}

	flat := mutate.Extract(img)
	defer flat.Close()

	tmp, err := os.CreateTemp(flatDir, "pull-*.tar")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, flat); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("flatten image: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, tarPath); err != nil {
		os.Remove(tmpPath)
		return "", "", err
	}

	log.Printf("image puller: pulled and flattened %s -> %s", imageRef, tarPath)
	return tarPath, digest, nil
}

// ExportImage saves a full OCI image to a tarball for debugging/inspection.
func ExportImage(ctx context.Context, imageRef string, outPath string) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return err
	}

	desc, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return err
	}

	img, err := desc.Image()
	if err != nil {
		return err
	}

	tag, ok := ref.(name.Tag)
	if !ok {
		tag = ref.Context().Tag("latest")
	}

	return tarball.WriteToFile(outPath, tag, img)
}
