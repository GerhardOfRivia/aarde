package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/config"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/internal/server"
)

func (c commandLine) serve(ctx context.Context, args []string) error {
	flags := newFlagSet("serve", c.stdout, "usage: aarde serve")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArguments(flags); err != nil {
		return err
	}
	return c.withConfig(ctx, func(cfg config.Config) error {
		tokenPath, err := cfg.TokenPath()
		if err != nil {
			return err
		}
		db, err := openDatabase(ctx, cfg, true)
		if err != nil {
			return err
		}
		defer db.Close()
		return server.Run(ctx, cfg.ListenAddress, catalog.New(db), c.version, tokenPath, cfg.WebPublicRead, cfg.WebBasemap, cfg.Viewer)
	})
}

func (c commandLine) inspect(ctx context.Context, args []string) error {
	flags := newFlagSet("inspect", c.stdout, "usage: aarde inspect <image.tif|image.ntf|image.nitf>")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArguments(flags, "image path"); err != nil {
		return err
	}
	return c.withConfig(ctx, func(config.Config) error {
		i, err := raster.Inspect(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		return encodeJSON(c.stdout, i)
	})
}

func encodeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
