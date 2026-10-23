package worker

import (
	"path/filepath"
	"slices"

	"github.com/Arman2122/p-ui/internal/node/assets"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

type FileAssetResolver = configuration.FileAssetResolver

type (
	fileAssetBackend interface {
		ResolveFileAssets(*configuration.Runtime, FileAssetResolver) (*configuration.Runtime, error)
	}
	fileAssetEngine interface {
		ResolveFileAssets([]byte, FileAssetResolver) ([]byte, error)
	}
)

func (w *Worker) SetAssetDirectory(directory string) { w.assetDirectory = filepath.Clean(directory) }

func (s *streamBackend) ResolveFileAssets(runtime *configuration.Runtime, resolve FileAssetResolver) (*configuration.Runtime, error) {
	engine, ok := s.engine.(fileAssetEngine)
	if !ok {
		if len(runtime.FileAssets) != 0 {
			return nil, configuration.ErrConfiguration
		}
		return runtime, nil
	}
	data, err := engine.ResolveFileAssets(runtime.Configuration, resolve)
	if err != nil {
		return nil, err
	}
	copy := *runtime
	copy.Configuration = data
	return &copy, nil
}

func (w *Worker) pruneAssets() error {
	if w.assets == nil {
		return nil
	}
	deployments, err := w.store.RetainedDeployments()
	if err != nil {
		return err
	}
	keep := slices.Clone(w.activeAssets)
	for _, deployment := range deployments {
		bundle, err := configuration.Decode(deployment.Payload)
		if err != nil {
			return err
		}
		for _, runtime := range bundle.Runtimes {
			if runtime.ID == w.id {
				for _, asset := range runtime.FileAssets {
					keep = append(keep, asset.SHA256)
				}
			}
		}
	}
	slices.Sort(keep)
	keep = slices.Compact(keep)
	_, err = w.assets.Prune(keep, configuration.MaxRuntimeFileAssets)
	return err
}

func runtimeAssetDigests(runtime *configuration.Runtime) []string {
	var digests []string
	if runtime != nil {
		for _, asset := range runtime.FileAssets {
			digests = append(digests, asset.SHA256)
		}
	}
	slices.Sort(digests)
	return slices.Compact(digests)
}

func (w *Worker) possibleActiveAssets(runtime *configuration.Runtime) ([]string, error) {
	digests := append(slices.Clone(w.activeAssets), runtimeAssetDigests(runtime)...)
	slices.Sort(digests)
	digests = slices.Compact(digests)
	if len(digests) > 2*configuration.MaxRuntimeFileAssets {
		return nil, configuration.ErrConfiguration
	}
	return digests, nil
}

func (w *Worker) resolveAssets(runtime *configuration.Runtime) (*configuration.Runtime, error) {
	if runtime == nil {
		return nil, nil
	}
	paths := map[string]string{}
	if len(runtime.FileAssets) != 0 {
		if w.assetDirectory == "" {
			return nil, configuration.ErrConfiguration
		}
		if w.assets == nil {
			var err error
			w.assets, err = assets.Open(w.assetDirectory)
			if err != nil {
				return nil, err
			}
		}
		if err := w.pruneAssets(); err != nil {
			return nil, err
		}
		var err error
		paths, err = w.assets.Stage(runtime.FileAssets)
		if err != nil {
			return nil, err
		}
	}
	backend, ok := w.backend.(fileAssetBackend)
	if !ok {
		if len(paths) != 0 {
			return nil, configuration.ErrConfiguration
		}
		return runtime, nil
	}
	used := make(map[string]bool, len(paths))
	resolved, err := backend.ResolveFileAssets(runtime, func(reference string) (string, error) {
		if _, _, err := configuration.ParseAssetReference(reference); err != nil {
			return "", err
		}
		path, ok := paths[reference]
		if !ok {
			return "", configuration.ErrConfiguration
		}
		used[reference] = true
		return path, nil
	})
	if err != nil {
		return nil, err
	}
	if len(used) != len(paths) {
		return nil, configuration.ErrConfiguration
	}
	return resolved, nil
}

func runtimeAssetDigest(id string, payload []byte) []byte {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil
	}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID == id {
			return configuration.RuntimeAssetManifest(id, runtime.FileAssets)
		}
	}
	return configuration.RuntimeAssetManifest(id, nil)
}
