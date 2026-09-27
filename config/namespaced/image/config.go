// Package image configures image.talos.m.inc4.net.
package image

import (
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
)

// Configure configures the image group.
func Configure(p *ujconfig.Provider) {
	p.AddResourceConfigurator("talos_image_factory_schematic", func(r *ujconfig.Resource) {
		r.ShortGroup = "image"
		r.Kind = "FactorySchematic"
	})
}
