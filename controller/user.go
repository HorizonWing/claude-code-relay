package controller

import (
	"claude-code-relay/common"
	"claude-code-relay/constant"
	"claude-code-relay/model"
	"claude-code-relay/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username         string `json:"username"`                                              // 用户名（与email二选一）
	Email            string `json:"email"`                                                 // 邮箱（与username二选一）
	Password         string `json:"password"`                                              // 密码（密码登录时必填）
	VerificationCode string `json:"verification_code"`                                     // 验证码（验证码登录时必填）
	LoginType        string `json:"login_type" binding:"required,oneof=password sms_code"` // 登录方式：password(密码) 或 sms_code(验证码)
}

type RegisterRequest struct {
	Username         string `json:"username" binding:"required"`
	Email            string `json:"email" binding:"required,email"`
	Password         string `json:"password" binding:"required,min=6"`
	VerificationCode string `json:"verification_code" binding:"required"`
}

type SendVerificationCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
	Type  string `json:"type" binding:"required"`
}

type ChangeEmailRequest struct {
	NewEmail         string `json:"new_email" binding:"required,email"`
	Password         string `json:"password" binding:"required"`
	VerificationCode string `json:"verification_code" binding:"required"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

type UpdateProfileRequest struct {
	Username string `json:"username"`
	Email    string `json:"email" binding:"omitempty,email"`
	Password string `json:"password" binding:"omitempty,min=6"`
}

type AdminCreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required"`
}

type AdminUpdateUserStatusRequest struct {
	Status *int `json:"status" binding:"required"`
}

// Login 用户登录（支持密码和验证码两种方式）
func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	// 参数验证
	if req.Username == "" && req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "用户名或邮箱必须提供其中一个",
			"code":  constant.InvalidParams,
		})
		return
	}

	// 根据登录方式验证必填字段
	if req.LoginType == "password" {
		if req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "密码不能为空",
				"code":  constant.InvalidParams,
			})
			return
		}
	} else if req.LoginType == "sms_code" {
		if req.VerificationCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "验证码不能为空",
				"code":  constant.InvalidParams,
			})
			return
		}
		// 验证码登录必须提供邮箱
		if req.Email == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "验证码登录必须提供邮箱",
				"code":  constant.InvalidParams,
			})
			return
		}
	}

	userService := service.NewUserService()
	var result *service.LoginResult
	var err error

	// 根据登录方式调用不同的登录方法
	if req.LoginType == "password" {
		if req.Username != "" {
			result, err = userService.LoginWithPassword(req.Username, "", req.Password, c)
		} else {
			result, err = userService.LoginWithPassword("", req.Email, req.Password, c)
		}
	} else if req.LoginType == "sms_code" {
		result, err = userService.LoginWithVerificationCode(req.Email, req.VerificationCode, c)
	}

	if err != nil {
		var code int
		switch err.Error() {
		case "用户名或密码错误", "邮箱或密码错误":
			code = constant.Unauthorized
		case "验证码错误", "验证码已过期或不存在":
			code = constant.Unauthorized
		case "账户已被禁用":
			code = constant.UserStatusAbnormal
		case "用户不存在":
			code = constant.NotFound
		default:
			code = constant.InternalServerError
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "登录成功",
		"code":    constant.Success,
		"data":    result,
	})
}

// SendVerificationCode 发送验证码
func SendVerificationCode(c *gin.Context) {
	var req SendVerificationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	var codeType common.VerificationCodeType
	switch req.Type {
	case "register":
		codeType = common.EmailVerification
	case "login":
		codeType = common.LoginVerification
	case "reset_password":
		codeType = common.PasswordReset
	case "change_email":
		codeType = common.EmailChange
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "验证码类型无效，支持: register, login, reset_password, change_email",
			"code":  constant.InvalidParams,
		})
		return
	}

	err := common.CheckVerificationCodeFrequency(req.Email, codeType)
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": err.Error(),
			"code":  constant.TooManyRequests,
		})
		return
	}

	_, err = common.SendVerificationCode(req.Email, codeType)
	if err != nil {
		common.SysError("发送验证码失败: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "验证码发送失败",
			"code":  constant.InternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "验证码已发送到您的邮箱",
		"code":    constant.Success,
	})
}

// Register 新用户注册
func Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	err := common.VerifyCode(req.Email, req.VerificationCode, common.EmailVerification)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  constant.InvalidParams,
		})
		return
	}

	userService := service.NewUserService()
	err = userService.Register(req.Username, req.Email, req.Password)
	if err != nil {
		var statusCode int
		var code int
		if err.Error() == "用户名已存在" || err.Error() == "邮箱已存在" {
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		} else {
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "注册成功",
		"code":    constant.Success,
	})
}

// GetProfile 获取用户信息
func GetProfile(c *gin.Context) {
	user := c.MustGet("user").(*model.User)

	userService := service.NewUserService()
	profile := userService.GetProfile(user)

	c.JSON(http.StatusOK, gin.H{
		"message": "获取成功",
		"code":    constant.Success,
		"data":    profile,
	})
}

// UpdateProfile 更新用户信息
func UpdateProfile(c *gin.Context) {
	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	user := c.MustGet("user").(*model.User)
	userService := service.NewUserService()

	err := userService.UpdateProfile(user, req.Username, req.Email, req.Password)
	if err != nil {
		var statusCode int
		var code int
		switch err.Error() {
		case "用户名已存在", "邮箱已存在":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		default:
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "更新成功",
		"code":    constant.Success,
	})
}

// ChangeEmail 更改绑定邮箱
func ChangeEmail(c *gin.Context) {
	var req ChangeEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	user := c.MustGet("user").(*model.User)
	userService := service.NewUserService()

	err := userService.ChangeEmail(user, req.NewEmail, req.Password, req.VerificationCode)
	if err != nil {
		var statusCode int
		var code int
		switch err.Error() {
		case "当前密码错误":
			statusCode = http.StatusBadRequest
			code = constant.Unauthorized
		case "验证码错误", "验证码已过期或不存在":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		case "邮箱已被其他用户使用":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		case "新邮箱不能与当前邮箱相同":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		default:
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "邮箱更改成功",
		"code":    constant.Success,
	})
}

// GetUsers 获取用户列表
func GetUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	userService := service.NewUserService()
	result, err := userService.GetUsers(page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
			"code":  constant.InternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "获取成功",
		"code":    constant.Success,
		"data":    result,
	})
}

// AdminCreateUser 管理员创建用户
func AdminCreateUser(c *gin.Context) {
	var req AdminCreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	userService := service.NewUserService()
	err := userService.AdminCreateUser(req.Username, req.Email, req.Password, req.Role)
	if err != nil {
		var statusCode int
		var code int
		switch err.Error() {
		case "用户名已存在", "邮箱已存在", "角色参数无效":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		default:
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "用户创建成功",
		"code":    constant.Success,
	})
}

// AdminUpdateUserStatus 管理员更新用户状态
func AdminUpdateUserStatus(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "用户ID参数无效",
			"code":  constant.InvalidParams,
		})
		return
	}

	var req AdminUpdateUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	userService := service.NewUserService()
	err = userService.AdminUpdateUserStatus(uint(userID), *req.Status)
	if err != nil {
		var statusCode int
		var code int
		switch err.Error() {
		case "用户不存在":
			statusCode = http.StatusNotFound
			code = constant.NotFound
		case "状态参数无效":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		default:
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "用户状态更新成功",
		"code":    constant.Success,
	})
}

// MenuItem 菜单项结构
type MenuItem struct {
	Path      string     `json:"path"`
	Name      string     `json:"name"`
	Component string     `json:"component"`
	Redirect  string     `json:"redirect,omitempty"`
	Meta      MenuMeta   `json:"meta"`
	Children  []MenuItem `json:"children,omitempty"`
}

type MenuMeta struct {
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// GetMenuList 获取用户菜单列表
func GetMenuList(c *gin.Context) {
	user := c.MustGet("user").(*model.User)

	// 构建菜单列表
	menus := buildMenuByRole(user.Role)

	c.JSON(http.StatusOK, gin.H{
		"message": "获取成功",
		"code":    constant.Success,
		"data": gin.H{
			"list": menus,
		},
	})
}

// buildMenuByRole 根据用户角色构建菜单
func buildMenuByRole(role string) []MenuItem {
	// 基础菜单（所有用户都有）
	baseMenus := []MenuItem{
		{
			Path:      "/group",
			Name:      "groups",
			Component: "LAYOUT",
			Redirect:  "/groups/list",
			Meta: MenuMeta{
				Title: "分组管理",
				Icon:  "usergroup",
			},
			Children: []MenuItem{
				{
					Path:      "list",
					Name:      "GroupsList",
					Component: "/groups/list/index",
					Meta: MenuMeta{
						Title: "分组列表",
					},
				},
			},
		},
		{
			Path:      "/accounts",
			Name:      "accounts",
			Component: "LAYOUT",
			Redirect:  "/accounts/list",
			Meta: MenuMeta{
				Title: "账号管理",
				Icon:  "user-circle",
			},
			Children: []MenuItem{
				{
					Path:      "list",
					Name:      "AccountsList",
					Component: "/accounts/list/index",
					Meta: MenuMeta{
						Title: "账号列表",
					},
				},
			},
		},
		{
			Path:      "/keys",
			Name:      "keys",
			Component: "LAYOUT",
			Redirect:  "/keys/list",
			Meta: MenuMeta{
				Title: "API密钥",
				Icon:  "secured",
			},
			Children: []MenuItem{
				{
					Path:      "list",
					Name:      "ApiKeysList",
					Component: "/keys/list/index",
					Meta: MenuMeta{
						Title: "密钥管理",
					},
				},
			},
		},
		{
			Path:      "/logs",
			Name:      "logs",
			Component: "LAYOUT",
			Redirect:  "/logs/my",
			Meta: MenuMeta{
				Title: "模型日志",
				Icon:  "chart-bar",
			},
			Children: []MenuItem{
				{
					Path:      "my",
					Name:      "MyLogs",
					Component: "/logs/my/index",
					Meta: MenuMeta{
						Title: "我的日志",
					},
				},
				{
					Path:      "stats",
					Name:      "LogStats",
					Component: "/logs/stats/index",
					Meta: MenuMeta{
						Title: "使用统计",
					},
				},
			},
		},
	}

	// 如果是管理员，添加管理员专属菜单
	if role == "admin" {
		adminMenus := []MenuItem{
			{
				Path:      "/admin",
				Name:      "admin",
				Component: "LAYOUT",
				Redirect:  "/admin/users",
				Meta: MenuMeta{
					Title: "系统管理",
					Icon:  "tools",
				},
				Children: []MenuItem{
					{
						Path:      "users",
						Name:      "AdminUsers",
						Component: "/admin/users/index",
						Meta: MenuMeta{
							Title: "用户管理",
						},
					},
					{
						Path:      "logs",
						Name:      "AdminLogs",
						Component: "/admin/logs/index",
						Meta: MenuMeta{
							Title: "系统日志",
						},
					},
					{
						Path:      "fallback",
						Name:      "AdminFallback",
						Component: "/admin/fallback/index",
						Meta: MenuMeta{
							Title: "Fallback管理",
						},
					},
				},
			},
		}
		baseMenus = append(baseMenus, adminMenus...)
	}

	return baseMenus
}

// ChangePassword 修改密码
func ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误",
			"code":  constant.InvalidParams,
		})
		return
	}

	user := c.MustGet("user").(*model.User)
	userService := service.NewUserService()

	err := userService.ChangePassword(user, req.OldPassword, req.NewPassword)
	if err != nil {
		var statusCode int
		var code int
		switch err.Error() {
		case "当前密码错误":
			statusCode = http.StatusBadRequest
			code = constant.Unauthorized
		case "新密码不能与当前密码相同":
			statusCode = http.StatusBadRequest
			code = constant.InvalidParams
		default:
			statusCode = http.StatusInternalServerError
			code = constant.InternalServerError
		}
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
			"code":  code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "密码修改成功",
		"code":    constant.Success,
	})
}
