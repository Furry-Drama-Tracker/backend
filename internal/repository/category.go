package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// CategoryRepo 分类仓储（categories 集合）。
type CategoryRepo struct{ coll *mongo.Collection }

// NewCategoryRepo 构造分类仓储。
func NewCategoryRepo(coll *mongo.Collection) *CategoryRepo { return &CategoryRepo{coll: coll} }

// FindAllSorted 查询全部分类，按 order 升序、createdAt 升序
// （find({}).sort({order:1, createdAt:1})）。
func (r *CategoryRepo) FindAllSorted(ctx context.Context) ([]model.Category, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "order", Value: 1}, {Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.Category
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// FindByName 按 name 精确查分类（唯一名检查用）；
// 不存在返回 ErrNotFound。
func (r *CategoryRepo) FindByName(ctx context.Context, name string) (*model.Category, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	c := &model.Category{}
	err := r.coll.FindOne(ctx, bson.M{"name": name}).Decode(c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return c, err
}

// FindByID 按 ID 查分类；不存在返回 ErrNotFound。
func (r *CategoryRepo) FindByID(ctx context.Context, id any) (*model.Category, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	c := &model.Category{}
	err := r.coll.FindOne(ctx, bson.M{"_id": ToObjectID(id)}).Decode(c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return c, err
}

// Create 插入分类；未显式设置时补 _id/createdAt
// （与既有集合文档的默认值形态一致）。唯一名冲突（E11000）原样返回，
// 由 handler 转为 500（既有 catch-all 错误路径，勿改）。
func (r *CategoryRepo) Create(ctx context.Context, c *model.Category) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if c.ID.IsZero() {
		c.ID = primitive.NewObjectID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	}
	_, err := r.coll.InsertOne(ctx, c)
	return err
}

// Save 整文档覆盖保存（__v 自增）。
func (r *CategoryRepo) Save(ctx context.Context, c *model.Category) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	c.VersionKey++
	_, err := r.coll.ReplaceOne(ctx, bson.M{"_id": c.ID}, c)
	return err
}

// DeleteByID 按 ID 删除分类。
func (r *CategoryRepo) DeleteByID(ctx context.Context, id any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": ToObjectID(id)})
	return err
}
