package image

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultRootfsSizeMB = 2048

// BuildRootfs creates an ext4 disk image from a flattened OCI tar, injecting
// the guest agent binary and init script. Returns the path to the ext4 image.
func BuildRootfs(flatTarPath string, agentBinPath string, initScriptPath string, outputPath string, sizeMB int) error {
	if sizeMB <= 0 {
		sizeMB = defaultRootfsSizeMB
	}

	log.Printf("rootfs: building %s (%d MB)", outputPath, sizeMB)

	if err := createBlankExt4(outputPath, sizeMB); err != nil {
		return fmt.Errorf("create ext4: %w", err)
	}

	mountDir, err := os.MkdirTemp("", "monza-rootfs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountDir)

	if err := mountExt4(outputPath, mountDir); err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	defer umount(mountDir)

	if err := extractTar(flatTarPath, mountDir); err != nil {
		return fmt.Errorf("extract tar: %w", err)
	}

	essentialDirs := []string{"proc", "sys", "dev", "dev/pts", "tmp", "run", "workspace"}
	for _, d := range essentialDirs {
		_ = os.MkdirAll(filepath.Join(mountDir, d), 0o755)
	}

	if agentBinPath != "" {
		dst := filepath.Join(mountDir, "usr/local/bin/monza-agent")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(agentBinPath, dst, 0o755); err != nil {
			return fmt.Errorf("copy agent: %w", err)
		}
	}

	if initScriptPath != "" {
		dst := filepath.Join(mountDir, "init")
		if err := copyFile(initScriptPath, dst, 0o755); err != nil {
			return fmt.Errorf("copy init: %w", err)
		}
	}

	log.Printf("rootfs: built %s", outputPath)
	return nil
}

func createBlankExt4(path string, sizeMB int) error {
	if err := exec.Command("truncate", "-s", fmt.Sprintf("%dM", sizeMB), path).Run(); err != nil {
		f, ferr := os.Create(path)
		if ferr != nil {
			return ferr
		}
		if ferr := f.Truncate(int64(sizeMB) * 1024 * 1024); ferr != nil {
			f.Close()
			return ferr
		}
		f.Close()
	}

	return exec.Command("mkfs.ext4", "-q", "-F", path).Run()
}

func mountExt4(imagePath, mountDir string) error {
	return exec.Command("mount", "-o", "loop", imagePath, mountDir).Run()
}

func umount(mountDir string) {
	_ = exec.Command("umount", mountDir).Run()
}

func extractTar(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	if strings.HasSuffix(tarPath, ".gz") || strings.HasSuffix(tarPath, ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			linkTarget := filepath.Join(destDir, hdr.Linkname)
			_ = os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
