package objstore

import (
	"errors"
	"sync"

	"utils"
)

// ErrNotConfigured is what Default answers when the settings of the object store are not all there.
var ErrNotConfigured = errors.New("objstore: no object store is set up (OPENREPL_S3_ENDPOINT, _BUCKET, _ACCESS_KEY_ID and _SECRET_ACCESS_KEY)")

var (
	defaultMu  sync.Mutex
	defaultCfg Config
	defaultCl  *Client
)

// FromSettings is the Config the server's settings describe (utils.S3*), and whether they are all set.
func FromSettings() (Config, bool) {
	return Config{
		Endpoint:  utils.S3Endpoint(),
		Region:    utils.S3Region(),
		Bucket:    utils.S3Bucket(),
		AccessKey: utils.S3AccessKey(),
		Secret:    utils.S3SecretKey(),
		Prefix:    utils.S3Prefix(),
	}, utils.S3Enabled()
}

// Default is the client of the settings, made again when a setting changes (a key saved in the dashboard).
func Default() (*Client, error) {
	cfg, ok := FromSettings()
	if !ok {
		return nil, ErrNotConfigured
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultCl != nil && defaultCfg == cfg {
		return defaultCl, nil
	}
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	defaultCfg, defaultCl = cfg, c
	return c, nil
}
