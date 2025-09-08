package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/CuteReimu/bilibili/v2"
	"github.com/go-sphere/confstore"
	"github.com/go-sphere/confstore/codec"
	"github.com/go-sphere/confstore/provider"
	"github.com/go-sphere/confstore/provider/file"
	"github.com/go-sphere/confstore/provider/http"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func main() {
	conf := flag.String("config", "config.json", "config file")
	flag.Parse()
	config, err := confstore.Load[Config](provider.NewSelect(*conf,
		provider.If(file.IsLocalPath, func(s string) provider.Provider {
			return file.New(s)
		}),
		provider.If(http.IsRemoteURL, func(s string) provider.Provider {
			return http.New(s, http.WithTimeout(10*time.Second))
		}),
	), codec.JsonCodec())
	if err != nil {
		log.Fatal(err)
	}
	app, err := NewBot(config)
	if err != nil {
		log.Fatal(err)
	}
	app.init()
	err = app.run()
	if err != nil {
		log.Fatal(err)
	}
}

type Bot struct {
	conf *Config

	status map[string]int
	bot    *bot.Bot
	client *bilibili.Client
}

func NewBot(conf *Config) (*Bot, error) {
	api, err := bot.New(conf.Token, bot.WithSkipGetMe())
	if err != nil {
		return nil, err
	}
	client := bilibili.New()
	store, err := confstore.Load[CacheStore](file.New(conf.CacheStore), codec.JsonCodec())
	if err == nil && store != nil {
		client.SetCookiesString(store.Cookie)
	}
	return &Bot{
		conf:   conf,
		status: make(map[string]int),
		bot:    api,
		client: client,
	}, nil
}

func (b *Bot) init() {
	_, _ = b.bot.DeleteWebhook(context.Background(), &bot.DeleteWebhookParams{})
	_, _ = b.bot.SetMyCommands(context.Background(), &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{
				Command:     "login",
				Description: "获取登录二维码",
			},
			{
				Command:     "check",
				Description: "检查最新视频",
			},
		},
	})
}

func (b *Bot) run() error {
	go b.startPulling()
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/login", bot.MatchTypeExact, wrapper(b.login))
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/check", bot.MatchTypeExact, wrapper(b.check))
	b.bot.Start(context.Background())
	return nil
}

func (b *Bot) startPulling() {
	for {
		time.Sleep(time.Minute)
		if b.client.GetCookiesString() == "" {
			break
		}
		text, err := b.loadLatestStatus(false)
		if err != nil {
			log.Printf("error: %v", err)
			continue
		}
		log.Printf("new video:\n%s", text)
		for _, chatID := range b.conf.Admins {
			_, _ = b.bot.SendMessage(context.Background(), &bot.SendMessageParams{
				ChatID: chatID,
				Text:   text,
			})
		}
	}
}

func (b *Bot) loadLatestStatus(skipCheck bool) (string, error) {
	videos, err := b.client.GetUserVideos(bilibili.GetUserVideosParam{
		Mid: b.conf.Mid,
		Ps:  1,
	})
	if err != nil {
		return "", err
	}
	if len(videos.List.Vlist) == 0 {
		return "", fmt.Errorf("no video found")
	}
	video := videos.List.Vlist[0]

	if play, ok := b.status[video.Bvid]; ok {
		latestPlay := strconv.Itoa(video.Play)
		currentPlay := strconv.Itoa(play)
		if !skipCheck &&
			len(currentPlay) > 0 &&
			len(latestPlay) > 0 &&
			len(currentPlay) == len(latestPlay) &&
			currentPlay[0] == latestPlay[0] {
			return "", fmt.Errorf("video not updated: %s(%d)", video.Bvid, video.Play)
		}
	}

	card, err := b.client.GetUserCard(bilibili.GetUserCardParam{
		Mid: b.conf.Mid,
	})
	if err != nil {
		return "", err
	}

	b.status[video.Bvid] = video.Play
	text := renderVideoInfo(video, card)
	return text, nil
}

func renderVideoInfo(video bilibili.UserVideo, card *bilibili.UserCard) string {
	text := fmt.Sprintf(`
播放量：%d
《%s》
弹幕数：%d
评论数：%d
链接：https://www.bilibili.com/video/%s

----

截止至 %s
你的粉丝数为%d
`, video.Play, video.Title, video.VideoReview, video.Comment, video.Bvid, time.Now().Format("2006-01-02 15:04:05"), card.Follower)
	return text
}

func (b *Bot) login(ctx context.Context, api *bot.Bot, update *models.Update) error {
	qrCode, err := b.client.GetQRCode()
	if err != nil {
		return err
	}
	buf, err := qrCode.Encode()
	if err != nil {
		return err
	}
	msg, err := api.SendPhoto(ctx, &bot.SendPhotoParams{
		ChatID: update.Message.Chat.ID,
		Photo: &models.InputFileUpload{
			Filename: "qr.png",
			Data:     bytes.NewReader(buf),
		},
	})
	if err != nil {
		return err
	}
	go func() {
		result, e := b.client.LoginWithQRCode(bilibili.LoginWithQRCodeParam{
			QrcodeKey: qrCode.QrcodeKey,
		})
		_, _ = api.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    msg.Chat.ID,
			MessageID: msg.ID,
		})
		if e != nil || result.Code != 0 {
			_ = replay(ctx, api, update, "登录失败")
		}
		raw, e := json.Marshal(&CacheStore{
			Cookie: b.client.GetCookiesString(),
		})
		if e != nil {
			return
		}
		e = os.WriteFile(b.conf.CacheStore, raw, 0o644)
		if e != nil {
			return
		}
		_ = replay(ctx, api, update, "登录成功")
	}()
	return nil
}

func (b *Bot) logout(ctx context.Context, api *bot.Bot, update *models.Update) error {
	b.client.SetCookiesString("")
	raw, err := json.Marshal(&CacheStore{
		Cookie: "",
	})
	if err != nil {
		return err
	}
	err = os.WriteFile(b.conf.CacheStore, raw, 0o644)
	if err != nil {
		return err
	}
	return replay(ctx, api, update, "已退出登录")
}

func (b *Bot) check(ctx context.Context, api *bot.Bot, update *models.Update) error {
	text, err := b.loadLatestStatus(true)
	if err != nil {
		return replay(ctx, api, update, err.Error())
	}
	return replay(ctx, api, update, text)
}

func replay(ctx context.Context, api *bot.Bot, update *models.Update, message string) error {
	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   message,
	})
	return err
}

func wrapper(handler func(context.Context, *bot.Bot, *models.Update) error) bot.HandlerFunc {
	return func(ctx context.Context, api *bot.Bot, update *models.Update) {
		err := handler(ctx, api, update)
		if err != nil {
			log.Printf("error: %v", err)
		}
	}
}
