package provider

import "context"

type Nvidia struct{}

func (Nvidia) Name() string { return "NVIDIA" }

func (Nvidia) CheckBalance(_ context.Context, _ string) (string, error) {
	return "N/A", nil
}
