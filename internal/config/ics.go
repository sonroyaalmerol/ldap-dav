package config

import "fmt"

type ICSConfig struct {
	CompanyName string
	ProductName string
	Version     string
	Language    string
}

func (cfg *ICSConfig) BuildProdID() string {
	if cfg.Version != "" {
		return fmt.Sprintf("-//%s//%s %s//%s",
			cfg.CompanyName, cfg.ProductName, cfg.Version, cfg.Language)
	}
	return fmt.Sprintf("-//%s//%s//%s",
		cfg.CompanyName, cfg.ProductName, cfg.Language)
}
