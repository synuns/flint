package db

import "context"

const LatestSchemaVersion = 21

type SchemaGate struct {
	Version int
	Ensure  func(ctx context.Context, r *Repo) error
}

func SchemaGates() []SchemaGate {
	return []SchemaGate{
		{2, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV2(ctx) }},
		{3, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV3(ctx) }},
		{4, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV4(ctx) }},
		{5, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV5(ctx) }},
		{6, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV6(ctx) }},
		{7, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV7(ctx) }},
		{8, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV8(ctx) }},
		{9, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV9(ctx) }},
		{10, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV10(ctx) }},
		{11, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV11(ctx) }},
		{12, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV12(ctx) }},
		{13, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV13(ctx) }},
		{14, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV14(ctx) }},
		{15, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV15(ctx) }},
		{16, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV16(ctx) }},
		{17, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV17(ctx) }},
		{18, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV18(ctx) }},
		{19, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV19(ctx) }},
		{20, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV20(ctx) }},
		{21, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV21(ctx) }},
	}
}
