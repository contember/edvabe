package template

import (
	"errors"

	"github.com/contember/edvabe/internal/sandbox"
)

// NewSandboxResolver adapts a Store so it satisfies
// sandbox.TemplateResolver, which the sandbox manager consults at
// Create time to turn `Sandbox.create('webmaster-chrome')` into an
// actual image tag.
//
// Resolution order (matches the Phase 3 scope):
//  1. Look up by UUID.
//  2. Look up by alias/name.
//  3. Return sandbox.ErrTemplateNotFound so the manager falls back
//     to its base image.
//
// The returned resolution carries the ImageTag persisted on the
// template record plus the template's StartCmd / ReadyCmd so the
// runtime can inject them as env vars into the container.
func NewSandboxResolver(store *Store) sandbox.TemplateResolver {
	return &sandboxResolver{store: store}
}

type sandboxResolver struct {
	store *Store
}

func (r *sandboxResolver) Resolve(idOrAlias string) (sandbox.TemplateResolution, error) {
	if r.store == nil {
		return sandbox.TemplateResolution{}, sandbox.ErrTemplateNotFound
	}
	tpl, err := r.store.ResolveNameOrID(idOrAlias)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return sandbox.TemplateResolution{}, sandbox.ErrTemplateNotFound
		}
		return sandbox.TemplateResolution{}, err
	}
	// Prefer the imageTag persisted by the build start handler. If
	// it's empty (e.g. the template record was created but no build
	// has started yet) we fall through to the sandbox manager's base
	// image via the empty-string signal.
	tag := tpl.ImageTag
	if tag == "" && tpl.LatestReady() != nil {
		tag = "edvabe/user-" + tpl.ID + ":latest"
	}
	return sandbox.TemplateResolution{
		ImageTag: tag,
		StartCmd: tpl.StartCmd,
		ReadyCmd: tpl.ReadyCmd,
		CPUCount: tpl.CPUCount,
		MemoryMB: tpl.MemoryMB,
	}, nil
}
