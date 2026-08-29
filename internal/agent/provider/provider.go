package provider

import "context"

type Checker interface {
	Name() string
	CheckBalance(ctx context.Context, apiKey string) (string, error)
}

// AccountBalanceChecker reports the provider's account-level available balance.
// CheckBalance may instead represent an API-key quota for backwards compatibility.
type AccountBalanceChecker interface {
	CheckAccountBalance(ctx context.Context, apiKey string) (string, error)
}

func Get(name string) Checker {
	switch name {
	case "siliconflow":
		return SiliconFlow{}
	case "openrouter":
		return OpenRouter{}
	case "deepseek":
		return DeepSeek{}
	case "ark":
		return ARK{}
	case "nvidia":
		return Nvidia{}
	}
	return nil
}
