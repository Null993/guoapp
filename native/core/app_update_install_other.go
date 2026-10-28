//go:build !windows

package core

import "errors"

func (e *nativeEngine) launchAppInstaller(state nativeUpdateState, rollback bool) error {
	return errors.New("当前平台请使用系统安装器完成安装")
}
