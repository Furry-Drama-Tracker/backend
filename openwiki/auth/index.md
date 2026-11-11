# 文件

- [账号安全能力](account-security.md) - 密码哈希与强度规则、密码修改与重置、带哈希备用码与一次性时间步的 TOTP 双因素认证、加密字段存储、邮箱验证与变更、通知偏好、账号注销以及个人数据导出。
- [注册与登录流程](login-and-registration.md) - 注册与登录的有序校验分支、Altcha 工作量证明、账号锁定、注销清理、邮箱与设备验证、带一次性时间步消费的 2FA 挑战、会话签发，以及串联这些流程的内存验证码存储。
- [鉴权中间件与角色](middleware-and-authorization.md) - Protect 中间件的准确失败结果与会话吊销诊断事件、角色常量与角色路由、默认超管邮箱的 RequireEmailChanged、可选鉴权、CSRF 双提交 cookie，以及 SSE 票据校验。
- [会话与令牌生命周期](session-and-token-lifecycle.md) - 双令牌模型、JWT claims 与 purpose、cookie 路径、带吊销原因墓碑的会话存储、refresh 轮换与 2 分钟宽限期重用检测、refresh 失败认证事件、419/409/401 语义、登出吊销以及会话管理端点。
