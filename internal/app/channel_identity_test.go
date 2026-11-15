package app

// 索引与频道的对应关系：索引文件按卷名命名，换过频道的卷必须重置。

import (
	"path/filepath"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/index"
)

// TestAdoptChannelResetsIndexOnChange 首次登记不动数据；频道变了清节点、账本与 pts。
func TestAdoptChannelResetsIndexOnChange(t *testing.T) {
	ctx := t.Context()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.PutNode(ctx, index.Node{ID: 5, ParentID: 0, Name: "a.bin", Path: "/a.bin"}); err != nil {
		t.Fatal(err)
	}
	if err := idx.PutMeta(ctx, index.MetaKeyChannelPts, 77); err != nil {
		t.Fatal(err)
	}
	if err := idx.PutPendingDone(ctx, index.PendingDone{Path: "/a.bin", MessageID: 5}); err != nil {
		t.Fatal(err)
	}

	a := New(config.Config{}, Options{})
	if err := a.adoptChannel(ctx, idx, 111); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	if _, ok, _ := idx.NodeByID(ctx, 5); !ok {
		t.Fatal("首次登记不该清节点")
	}
	if v, ok, _ := idx.GetMeta(ctx, index.MetaKeyChannelPts); !ok || v != 77 {
		t.Fatalf("首次登记不该动 pts, got %d", v)
	}

	if err := a.adoptChannel(ctx, idx, 222); err != nil {
		t.Fatalf("换频道: %v", err)
	}
	if _, ok, _ := idx.NodeByID(ctx, 5); ok {
		t.Fatal("换频道后节点应清空")
	}
	if v, ok, _ := idx.GetMeta(ctx, index.MetaKeyChannelPts); ok && v != 0 {
		t.Fatalf("换频道后 pts 应归零, got %d", v)
	}
	if _, ok, _ := idx.GetPendingDone(ctx, "/a.bin"); ok {
		t.Fatal("换频道后已上传账本应清空（里面是旧频道的消息号）")
	}
	if v, ok, _ := idx.GetMeta(ctx, index.MetaKeyChannelID); !ok || v != 222 {
		t.Fatalf("新频道 id 未记录: v=%d ok=%v", v, ok)
	}
}
