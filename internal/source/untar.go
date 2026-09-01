package source

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// untar extracts a tar stream (as produced by `git archive`) into dest.
// Entry names are validated against path traversal; symlinks are recreated
// only when their target stays inside dest, and unknown entry types are
// skipped. File permission bits are preserved so executables stay runnable.
func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
			continue
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// A link escaping dest would let a later scan walk outside the
			// materialized tree; skip those, keep in-tree links.
			if _, err := safeJoin(filepath.Dir(target), hdr.Linkname); err != nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			// Hardlinks, devices, fifos: git archive does not produce them;
			// skipping is safer than guessing.
		}
	}
}

// safeJoin joins name under dest, rejecting absolute names and traversal.
func safeJoin(dest, name string) (string, error) {
	if filepath.IsAbs(name) || filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("archive entry %q: absolute path", name)
	}
	target := filepath.Join(dest, filepath.FromSlash(name))
	base := filepath.Clean(dest)
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the extraction root", name)
	}
	return target, nil
}
