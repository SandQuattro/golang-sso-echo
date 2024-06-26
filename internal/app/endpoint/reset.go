package endpoint

import (
	"crypto/rand"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"sso/internal/app/crypto"
	"sso/internal/app/errs"
	"sso/internal/app/interfaces"
	"sso/internal/app/utils"

	logdoc "github.com/LogDoc-org/logdoc-go-appender/logrus"
	"github.com/gurkankaymak/hocon"
	"github.com/labstack/echo-contrib/jaegertracing"
	"github.com/labstack/echo/v4"
	"github.com/opentracing/opentracing-go"

	"strings"
)

type ResetEndpoint struct {
	config func() *hocon.Config
	users  interfaces.UserService
}

func NewResetEndpoint(config func() *hocon.Config, users interfaces.UserService) *ResetEndpoint {
	// Создаем endpoint и возвращаем
	return &ResetEndpoint{config, users}
}

func (e *ResetEndpoint) ResetHandler(ctx echo.Context) error {
	logger := logdoc.GetLogger()
	logger.Info(">> ResetHandler started..")

	span := jaegertracing.CreateChildSpan(ctx, "password reset handler")
	defer span.Finish()

	email := make(map[string]string)
	err := ctx.Bind(&email)
	if err != nil {
		return err
	}
	if _, ok := email["email"]; !ok {
		logger.Error("<< ResetHandler error, email parameter required")
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}

	c := opentracing.ContextWithSpan(ctx.Request().Context(), span)
	// проверяем наличие пользователя с такой почтой
	user, err := e.users.FindUserByLogin(c, email["email"])
	if err != nil {
		logger.Error(fmt.Errorf("user searching error, reason: %s", err.Error()))
		logger.Info("<< ResetHandler done.")
		return APIErrorSilent(http.StatusBadRequest, errs.UserGettingError)
	}

	if user == nil {
		logger.Error(fmt.Errorf("user not found"))
		logger.Info("<< ResetHandler done.")
		return APIErrorSilent(http.StatusBadRequest, errs.UserGettingError)
	}

	code := crypto.GenerateCode(20)
	err = utils.CreateTask(e.config, span, utils.TypePasswordReset, user.Name, user.Email, -1, fmt.Sprintf("%s%s", strings.ReplaceAll(e.config().GetString("verification.reset.address"), "\"", ""), code))
	if err != nil {
		logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
		return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
	}

	// записываем в БД код для подтверждения почты и user_id
	// после верификации почты меняется статус на активированный
	err = e.users.CreateUserNotification(c, user.ID, "password_reset", code)
	if err != nil {
		logger.Error("<< CreateHandler error, creating user notification", err)
		return APIErrorSilent(http.StatusBadRequest, errs.CreateUserNotificationError)
	}

	err = utils.CreateTask(e.config, span, utils.TypeTelegramDelivery, user.Email, user.Email, user.ID, fmt.Sprintf("User password requested: %s, IP: %s", user.Email, ctx.RealIP()))
	if err != nil {
		logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
		return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
	}

	logger.Info("<< ResetHandler done.")
	return APISuccess(http.StatusOK, map[string]string{"message": "на указанную вами почту направлено письмо для сброса пароля"})
}

func (e *ResetEndpoint) PasswordResetValidateHandler(ctx echo.Context) error {
	logger := logdoc.GetLogger()
	logger.Info(">> PasswordResetHandler started..")

	span := jaegertracing.CreateChildSpan(ctx, "password reset handler")
	defer span.Finish()

	pwdReset := make(map[string]string)
	err := ctx.Bind(&pwdReset)
	if err != nil {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	code, ok := pwdReset["code"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	pwd1, ok := pwdReset["pwd1"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	pwd2, ok := pwdReset["pwd2"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	if pwd1 != pwd2 {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}

	c := opentracing.ContextWithSpan(ctx.Request().Context(), span)

	notification, err := e.users.GetUserNotificationByTypeAndCode(c, "password_reset", code)
	if err != nil {
		return APIErrorSilent(http.StatusBadRequest, errs.GettingUserNotificationError)
	}

	if notification == nil {
		return APIErrorSilent(http.StatusBadRequest, errs.GettingUserNotificationError)
	}

	user, err := e.users.FindUserById(c, notification.UserID)
	if err != nil {
		logger.Error(fmt.Errorf("user searching error, reason: %s", err.Error()))
		return APIErrorSilent(http.StatusBadRequest, errs.UserGettingError)
	}

	if user == nil {
		return APIErrorSilent(http.StatusBadRequest, errs.UserGettingError)
	}

	salt := make([]byte, 8)
	_, err = rand.Read(salt)
	if err != nil {
		logger.Error("Ошибка заполнения salt slice, ", err.Error())
		span.SetTag("error", true)
		return APIErrorSilent(http.StatusBadRequest, errs.InternalProcessingError)
	}

	hashedPwd := crypto.HashArgon2(salt, pwd1, 32)

	err = e.users.UpdateUserPassword(c, code, notification.UserID, hashedPwd)
	if err != nil {
		logger.Error("<< PasswordResetHandler error", err)
		return APIErrorSilent(http.StatusBadRequest, errs.PasswordUpdateError)
	}

	err = utils.CreateTask(e.config, span, utils.TypeTelegramDelivery, user.Email, user.Email, user.ID, fmt.Sprintf("User password reset success: %s, IP: %s", user.Email, ctx.RealIP()))
	if err != nil {
		logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
		return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
	}

	logger.Info("<< PasswordResetHandler done")
	return APISuccess(http.StatusOK, map[string]string{"message": "пароль успешно изменен"})
}

func (e *ResetEndpoint) PasswordChangeHandler(ctx echo.Context) error {
	logger := logdoc.GetLogger()
	logger.Info(">> PasswordChangeHandler started..")

	span := jaegertracing.CreateChildSpan(ctx, "password change handler")
	defer span.Finish()

	pwdChange := make(map[string]string)
	err := ctx.Bind(&pwdChange)
	if err != nil {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	current, ok := pwdChange["current"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	pwd1, ok := pwdChange["pwd1"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	pwd2, ok := pwdChange["pwd2"]
	if !ok {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}
	if pwd1 != pwd2 {
		return APIErrorSilent(http.StatusBadRequest, errs.InvalidInputData)
	}

	c := opentracing.ContextWithSpan(ctx.Request().Context(), span)

	claims := ctx.Get("claims").(jwt.MapClaims)
	email := claims["adr"].(string)

	u, err := e.users.FindUserByLogin(c, email)
	if err != nil {
		logger.Error(fmt.Errorf("user not found, reason: %s", err.Error()))
		logger.Info("<< PasswordChangeHandler done.")
		return APIErrorSilent(http.StatusForbidden, errs.UserGettingError)
	}

	if u == nil {
		span.SetTag("error", true)
		span.LogKV("error.message", "пользователь "+email+" отсутствует в БД")
		err := utils.CreateTask(e.config, span, utils.TypeTelegramDelivery, email, email, u.ID, fmt.Sprintf("Attention required! Error in password change service, user: %s not found in database\nIP:%s", email, ctx.RealIP()))
		if err != nil {
			logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
			return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
		}
		return APIErrorSilent(http.StatusForbidden, errs.AccessDenied)
	}

	// Сначала проверяем пароль на корректность
	if !crypto.ComparePass(u.Password, current) {
		err := utils.CreateTask(e.config, span, utils.TypeTelegramDelivery, u.Name, u.Email, u.ID, fmt.Sprintf("Attention required! Error in password change service, invalid current password for user %s\nIP:%s", u.Email, ctx.RealIP()))
		if err != nil {
			logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
			return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
		}
		return APIErrorSilent(http.StatusForbidden, errs.AccessDenied)
	}

	// затем подтверждена ли почта
	if !u.EmailVerified {
		return APIErrorSilent(http.StatusForbidden, errs.AccessDenied)
	}

	salt := make([]byte, 8)
	_, err = rand.Read(salt)
	if err != nil {
		logger.Error("Ошибка заполнения salt slice, ", err.Error())
		span.SetTag("error", true)
		return APIErrorSilent(http.StatusBadRequest, errs.InternalProcessingError)
	}

	hashedPwd := crypto.HashArgon2(salt, pwd1, 32)

	err = e.users.UpdateUserPassword(c, "changing password", u.ID, hashedPwd)
	if err != nil {
		logger.Error("<< PasswordChangeHandler error", err)
		return APIErrorSilent(http.StatusBadRequest, errs.PasswordUpdateError)
	}

	err = utils.CreateTask(e.config, span, utils.TypeTelegramDelivery, email, email, u.ID, fmt.Sprintf("User password changed successfully: %s, IP: %s", u.Email, ctx.RealIP()))
	if err != nil {
		logger.Error(fmt.Errorf("error creating task: %s", err.Error()))
		return APIErrorSilent(http.StatusInternalServerError, errs.TaskCreationError)
	}

	logger.Info("<< PasswordChangeHandler done")
	return APISuccess(http.StatusOK, map[string]string{"message": "пароль успешно изменен"})
}
