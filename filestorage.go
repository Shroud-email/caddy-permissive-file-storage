package customstorage

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/filestorage"
	"github.com/caddyserver/certmagic"
)

func init() {
	caddy.RegisterModule(PermissiveStorage{})
}

type CertmagicStorage struct {
	certmagic.FileStorage
}

type PermissiveStorage struct {
	filestorage.FileStorage
}

func (PermissiveStorage) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "caddy.storage.permissive_file_storage",
		New: func() caddy.Module { return new(PermissiveStorage) },
	}
}

// Override Store to use globally-readable permissions
func (fs *CertmagicStorage) Store(key string, value []byte) error {
	filename := fs.Filename(key)
	err := os.MkdirAll(filepath.Dir(filename), 0755)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, value, 0644)
}
