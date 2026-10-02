package module

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"context"
)

// DownloadOptions controls Download.
type DownloadOptions struct {
	Dir   string // destination directory (created if needed)
	Force bool   // replace an existing file whose content differs
}

// DownloadedFile is the outcome for one file.
type DownloadedFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"` // local path
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA1   string `json:"sha1,omitempty"`
	Status string `json:"status"` // downloaded | skipped | failed
	Error  string `json:"error,omitempty"`
}

type DownloadResult struct {
	Dir        string           `json:"dir"`
	Files      []DownloadedFile `json:"files"`
	Downloaded int              `json:"downloaded"`
	Skipped    int              `json:"skipped"`
	Failed     int              `json:"failed"`
	Bytes      int64            `json:"bytes"`
}

// progressWriter reports each written chunk.
type progressWriter struct{ rep Reporter }

func (p progressWriter) Write(b []byte) (int, error) {
	p.rep.AssetBytes(int64(len(b)))
	return len(b), nil
}

// Download fetches files into opts.Dir. Each file goes to "<name>.part", is checked against
// the SHA-1 Nexus reports (when known), then renamed: an interrupted or corrupted transfer
// never leaves a file that looks complete. An identical file already present is skipped; a
// different one is an error unless opts.Force. A partial failure returns ErrPartial.
func Download(ctx context.Context, t Target, files []FileDetail, opts DownloadOptions, rep Reporter) (*DownloadResult, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("dossier de destination : %w", err)
	}
	res := &DownloadResult{Dir: dir}
	for _, f := range files {
		df := DownloadedFile{Name: f.Name, URL: f.URL, Size: f.Size, SHA1: f.SHA1}
		name := filepath.Base(f.Name)
		if name == "" || name == "." || name == ".." || name != f.Name || strings.ContainsAny(name, `/\`) {
			df.Status, df.Error = "failed", fmt.Sprintf("nom de fichier refusé : %q", f.Name)
		} else {
			df.Path = filepath.Join(dir, name)
			fetchOne(ctx, t, f, &df, opts, rep)
		}
		switch df.Status {
		case "downloaded":
			res.Downloaded++
			res.Bytes += df.Size
		case "skipped":
			res.Skipped++
		default:
			res.Failed++
		}
		res.Files = append(res.Files, df)
		if ctx.Err() != nil {
			break
		}
	}
	if res.Failed > 0 {
		return res, ErrPartial
	}
	return res, nil
}

func fetchOne(ctx context.Context, t Target, f FileDetail, df *DownloadedFile, opts DownloadOptions, rep Reporter) {
	fail := func(err error) { df.Status, df.Error = "failed", err.Error() }
	if st, err := os.Stat(df.Path); err == nil {
		if st.IsDir() {
			fail(fmt.Errorf("%s est un dossier", df.Path))
			return
		}
		if f.SHA1 != "" {
			if have, err := fileSHA1(df.Path); err == nil && strings.EqualFold(have, f.SHA1) {
				df.Status, df.Size = "skipped", st.Size()
				return
			}
		}
		if !opts.Force {
			fail(fmt.Errorf("%s existe déjà avec un contenu différent (--force pour le remplacer)", df.Path))
			return
		}
	}
	rep.AssetStart(f.Name, f.Size)
	err := transfer(ctx, t, f, df, rep)
	rep.AssetDone(f.Name, err)
	if err != nil {
		fail(err)
		return
	}
	df.Status = "downloaded"
}

func transfer(ctx context.Context, t Target, f FileDetail, df *DownloadedFile, rep Reporter) (err error) {
	part := df.Path + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(part)
		}
	}()
	h := sha1.New()
	n, err := t.Client.Download(ctx, f.URL, io.MultiWriter(out, h, progressWriter{rep}))
	if err != nil {
		return fmt.Errorf("téléchargement : %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if f.SHA1 != "" && !strings.EqualFold(got, f.SHA1) {
		return fmt.Errorf("sha1 reçu %s ≠ sha1 annoncé par Nexus %s : fichier supprimé", got, f.SHA1)
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(part, df.Path); err != nil {
		return err
	}
	df.Size, df.SHA1 = n, got
	return nil
}

func fileSHA1(p string) (string, error) {
	fh, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha1.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
