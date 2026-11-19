package telegramstore

// APIProxy 单测：未接线时一律 ErrOffline，Set 之后原样转发。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// TestAPIProxyOfflineBeforeSet 未接线时所有 RPC 都返回 ErrOffline。
//
// 输入：新建代理，不 Set
// 输出：三个不同返回形状的方法都拿到 ErrOffline
// 预期：本地挂载阶段不会误连网络，也不会伪装成成功
func TestAPIProxyOfflineBeforeSet(t *testing.T) {
	p := NewAPIProxy()
	if !p.Offline() {
		t.Fatal("新代理应处于离线状态")
	}
	ctx := context.Background()
	if _, err := p.ChannelsGetChannels(ctx, nil); !errors.Is(err, ErrOffline) {
		t.Fatalf("ChannelsGetChannels err = %v, want ErrOffline", err)
	}
	if _, err := p.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{}); !errors.Is(err, ErrOffline) {
		t.Fatalf("UploadSaveFilePart err = %v, want ErrOffline", err)
	}
	if _, err := p.UploadGetFileHashes(ctx, &tg.UploadGetFileHashesRequest{}); !errors.Is(err, ErrOffline) {
		t.Fatalf("UploadGetFileHashes err = %v, want ErrOffline", err)
	}
}

// TestAPIProxyForwardsAfterSet 接线后转发到真实 API。
//
// 输入：Set(fakeAPI)
// 输出：ChannelsGetChannels 真的落到 fake 上
// 预期：Store 不必重建即换到真实客户端
func TestAPIProxyForwardsAfterSet(t *testing.T) {
	p := NewAPIProxy()
	fake := newFakeAPI()
	p.Set(fake)
	if p.Offline() {
		t.Fatal("Set 之后不应再是离线状态")
	}
	if _, err := p.ChannelsGetChannels(context.Background(), nil); err != nil {
		t.Fatalf("转发失败：%v", err)
	}
	if fake.channelCalls != 1 {
		t.Fatalf("channelCalls = %d, want 1", fake.channelCalls)
	}
}

// TestAPIProxySetNilGoesOffline 传 nil 等价于断线。
//
// 输入：Set 真实 API 后再 Set(nil)
// 输出：后续调用回到 ErrOffline
// 预期：断网/连接循环退出后不会再打到已关闭的客户端
func TestAPIProxySetNilGoesOffline(t *testing.T) {
	p := NewAPIProxy()
	p.Set(newFakeAPI())
	p.Set(nil)
	if !p.Offline() {
		t.Fatal("Set(nil) 之后应回到离线状态")
	}
	if _, err := p.ChannelsGetChannels(context.Background(), nil); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v, want ErrOffline", err)
	}
}
