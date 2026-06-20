package provider

import "context"

type Checker interface {
	Name() string
	CheckBalance(ctx context.Context, apiKey string) (string, error)
}

func Get(name string) Checker {
	switch name {
	case "siliconflow":
		return SiliconFlow{}
	case "openrouter":
		return OpenRouter{}
	case "ark":
		return ARK{}
	case "nvidia":
		return Nvidia{}
	}
	return nil
}