package backend

import (
	"errors"
)

type PlatformService struct {
	OpenURLFunc       func(string) error
	RevealItemFunc    func(string) error
	OpenFileFunc      func(string, ...string) error
	OpenDirectoryFunc func(OpenDirectoryOptions) (any, error)
}

type OpenDirectoryOptions struct {
	Title    string `json:"title"`
	Multiple bool   `json:"multiple"`
}

func (s *PlatformService) OpenURL(url string) error {
	if s.OpenURLFunc == nil {
		return errors.New("platform service is not ready")
	}
	return s.OpenURLFunc(url)
}

func (s *PlatformService) RevealItemInDir(path string) error {
	if s.RevealItemFunc == nil {
		return errors.New("platform service is not ready")
	}
	return s.RevealItemFunc(path)
}

func (s *PlatformService) RevealPlanPath(path string) error {
	if s.OpenFileFunc == nil {
		return errors.New("platform service is not ready")
	}
	return s.OpenFileFunc("open", "-R", path)
}

func (s *PlatformService) OpenDirectory(options OpenDirectoryOptions) (any, error) {
	if s.OpenDirectoryFunc == nil {
		return nil, errors.New("platform service is not ready")
	}
	return s.OpenDirectoryFunc(options)
}
