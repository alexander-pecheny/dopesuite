package kit

import base "pecheny.me/dopeuikit/ui"

// The kit is the app-facing API: it re-exports the engine's expander surface so
// app code (and app-generated builders) import this one package, never ui
// directly. The node types and builder leaves are re-exported by the generated
// tags_gen.go; the engine helpers and framework types are re-exported here.

// Framework types.
type (
	ExpandCtx     = base.ExpandCtx
	ExpandFunc    = base.ExpandFunc
	InlineFunc    = base.InlineFunc
	MountSpec     = base.MountSpec
	Chrome        = base.Chrome
	PageKind      = base.PageKind
	SyncSpec      = base.SyncSpec
	PropSpec      = base.PropSpec
	EnumSpec      = base.EnumSpec
	PrimitiveSpec = base.PrimitiveSpec
	Vocab         = base.Vocab
	App           = base.App
	Error         = base.Error
)

// Engine helper surface (class-free prop/attr helpers).
var (
	El          = base.El
	Inl         = base.Inl
	ClassAttr   = base.ClassAttr
	At          = base.At
	BareAt      = base.BareAt
	Get         = base.Get
	Flag        = base.Flag
	IDAttr      = base.IDAttr
	Passthrough = base.Passthrough
	CopyProps   = base.CopyProps
	CopyFlags   = base.CopyFlags
	MetaAttrs   = base.MetaAttrs
	LoadVocab   = base.LoadVocab
	Parse       = base.Parse
	Validate    = base.Validate
)
