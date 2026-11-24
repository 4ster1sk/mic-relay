# Mic Relay (Linux ↔ Windows 双方向マイク中継)

Linux（Debian 12 以降 / PipeWire）と Windows（VB-Cable / VRChat）の間で低遅延・高安定にマイク音声を双方向中継するための Go 製 GUI アプリケーションです。

## 主な機能
- **双方向対応（単一統合アプリ `mic-relay`）**:
  - **Linux → Windows**: PipeWire マイクの音声を Windows の VB-Cable へ出力（VRChat のマイク入力に利用）。
  - **Windows → Linux**: Windows のマイク音声を Linux の PipeWire 仮想マイク（`pw-loopback` で `Mic Relay Virtual Mic` を自動作成・停止時自動削除）へ出力。
  - 片方向のみ・両方向同時（フルデュプレックス）どちらも独立操作可能。
- **Pure-Go RNNoise ノイズ抑制**: 48kHz / 10ms フレーム直結（追加遅延ゼロ）で GUI / トレイから即時 ON/OFF 切替・VAD 表示に対応。
- **圧縮モードのリアルタイム切替**:
  - **非圧縮 PCM（48kHz Mono 16bit / 10ms, 768 kbps）**: 同一 LAN 向け・コーデック遅延 0ms。
  - **Opus 96 kbps / 48 kbps（Pure Go, 20ms）**: Wi-Fi や Tailscale 経由向け。送信中の切替に受信側が自動追従。
- **同一 LAN & Tailscale 両対応**:
  - LAN 自動検出（UDP ブロードキャスト `48100`）、Tailscale ピア自動候補（`tailscale status --json`）、MagicDNS/IP 手動指定・プリセット保存・UDP Ping 疎通確認。
- **長時間通話向けのクロックドリフト補正**: ジッターバッファ（目標 10〜150ms）＋ EWMA 適応型リサンプリングで、OS/ハードウェア間のクロック差による音切れ・遅延増大を防止。
- **システムトレイ最小化モード**: `×` ボタンでトレイに格納してバックグラウンド中継を継続。

## クイックスタート

### Linux (Debian 12 以降)
```bash
sudo apt install ./mic-relay_<version>_amd64.deb
mic-relay
```
- PipeWire 環境では `pipewire-pulse` と `pipewire-bin`（`pw-loopback`）の導入を推奨します（`.deb` の `Recommends` に記載済み）。

### Windows
1. [VB-Cable (VB-Audio Virtual Cable)](https://vb-audio.com/Cable/) をインストールします。
2. `mic-relay.exe` を起動し、初回起動時の Windows ファイアウォール確認で「プライベートネットワーク」を許可します（UDP `48100` / `48101`）。
3. 受信 (RX) タブで出力先に **`[推奨] CABLE Input (VB-Audio Virtual Cable)`** を選択し、「受信待機を開始」をクリックします。
4. VRChat のオーディオ設定でマイク入力に **`CABLE Output (VB-Audio Virtual Cable)`** を選択します。

## 開発・テスト・ビルド
```bash
go vet ./...
go test -v -race ./...
make deb
```
Gitea Actions（`.gitea/workflows/build.yaml`）により、`master` ブランチおよび `v*` タグの push 時に Debian 12+ 互換 `.deb` と Windows `.exe` が自動ビルドされます。
