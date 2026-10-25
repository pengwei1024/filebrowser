package fbhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/filebrowser/filebrowser/v2/rules"
	"github.com/filebrowser/filebrowser/v2/settings"
)

type settingsData struct {
	Signup                bool                  `json:"signup"`
	HideLoginButton       bool                  `json:"hideLoginButton"`
	CreateUserDir         bool                  `json:"createUserDir"`
	MinimumPasswordLength uint                  `json:"minimumPasswordLength"`
	UserHomeBasePath      string                `json:"userHomeBasePath"`
	Defaults              settings.UserDefaults `json:"defaults"`
	AuthMethod            settings.AuthMethod   `json:"authMethod"`
	Rules                 []rules.Rule          `json:"rules"`
	Branding              settings.Branding     `json:"branding"`
	Tus                   settings.Tus          `json:"tus"`
	Shell                 []string              `json:"shell"`
	Commands              map[string][]string   `json:"commands"`
	LoginAlertToken       string                `json:"loginAlertToken"`
	LoginAlertGroup       int                   `json:"loginAlertGroup"`
}

var settingsGetHandler = withAdmin(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	data := &settingsData{
		Signup:                d.settings.Signup,
		HideLoginButton:       d.settings.HideLoginButton,
		CreateUserDir:         d.settings.CreateUserDir,
		MinimumPasswordLength: d.settings.MinimumPasswordLength,
		UserHomeBasePath:      d.settings.UserHomeBasePath,
		Defaults:              d.settings.Defaults,
		AuthMethod:            d.settings.AuthMethod,
		Rules:                 d.settings.Rules,
		Branding:              d.settings.Branding,
		Tus:                   d.settings.Tus,
		Shell:                 d.settings.Shell,
		Commands:              d.settings.Commands,
		LoginAlertToken:       d.settings.LoginAlertToken,
		LoginAlertGroup:       d.settings.LoginAlertGroup,
	}

	return renderJSON(w, r, data)
})

var settingsPutHandler = withAdmin(func(_ http.ResponseWriter, r *http.Request, d *data) (int, error) {
	req := &settingsData{}
	err := json.NewDecoder(r.Body).Decode(req)
	if err != nil {
		return http.StatusBadRequest, err
	}

	d.settings.Signup = req.Signup
	d.settings.CreateUserDir = req.CreateUserDir
	d.settings.MinimumPasswordLength = req.MinimumPasswordLength
	d.settings.UserHomeBasePath = req.UserHomeBasePath
	d.settings.Defaults = req.Defaults
	d.settings.Rules = req.Rules
	d.settings.Branding = req.Branding
	d.settings.Tus = req.Tus
	d.settings.Shell = req.Shell
	d.settings.Commands = req.Commands
	d.settings.HideLoginButton = req.HideLoginButton
	d.settings.LoginAlertToken = req.LoginAlertToken
	d.settings.LoginAlertGroup = req.LoginAlertGroup

	err = d.store.Settings.Save(d.settings)
	return errToStatus(err), err
})

// alertTestHandler sends a one-off test message to the Baidu IM destination the
// admin typed into the Settings form (which may not be saved yet), so they can
// confirm the token and group work before relying on the throttle to use them.
var alertTestHandler = withAdmin(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	req := &struct {
		LoginAlertToken string `json:"loginAlertToken"`
		LoginAlertGroup int    `json:"loginAlertGroup"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBodySize)).Decode(req); err != nil {
		return http.StatusBadRequest, err
	}
	if !alertConfigured(req.LoginAlertToken, req.LoginAlertGroup) {
		return http.StatusBadRequest, errors.New("access_token 和群号都必须填写")
	}

	content := "[File Browser] 测试消息：登录爆破报警配置成功，发送时间 " +
		time.Now().Format("2006-01-02 15:04:05") + "。"
	if err := newAlerter().send(req.LoginAlertToken, req.LoginAlertGroup, content); err != nil {
		http.Error(w, "发送失败："+err.Error(), http.StatusBadGateway)
		return 0, nil
	}
	return http.StatusOK, nil
})
