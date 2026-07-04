package i18n

// en maps Russian source texts to their English translations. Keys must match
// the source strings byte-for-byte; missing entries fall back to Russian.
var en = map[string]string{
	// --- notify ---
	"⏰ %s, ваша подписка истекает %s — через %d %s.\nДля продления оплатите подписку.":             "⏰ %s, your subscription expires on %s — in %d %s.\nPlease pay to renew it.",
	"⛔️ %s, ваша подписка истекла %s.\nЧтобы продолжить пользоваться сервисом, продлите подписку.": "⛔️ %s, your subscription expired on %s.\nRenew it to keep using the service.",

	// --- telegram (welcome / shared buttons) ---
	"👤 Личный кабинет":    "👤 My account",
	"🔗 Привязать аккаунт": "🔗 Link account",
	WelcomeRU:             welcomeEN,

	// --- main (command menu / reply keyboard) ---
	"Кнопка «%s» теперь всегда под полем ввода 👇": "The “%s” button is now always under the input field 👇",
	"Личный кабинет":                    "My account",
	"Открыть меню":                      "Open the menu",
	"Посмотреть тарифы":                 "Show plans",
	"Подарить подписку":                 "Gift a subscription",
	"Мои подарочные подписки":           "My gift subscriptions",
	"Пригласить нового пользователя":    "Invite a new user",
	"Привязать свой Telegram к профилю": "Link your Telegram to a profile",
	"Активировать пробный период":       "Start free trial",
	"Отменить текущее действие":         "Cancel the current action",
	"Помощь": "Help",
	"О боте": "About the bot",
	"Панель администратора": "Admin panel",
	"Статистика":            "Statistics",
	"Резервная копия базы данных": "Database backup",
}

// The welcome message is kept as named constants so the map stays readable.
// WelcomeRU is exported so the telegram package can reference the same string
// that serves as the translation key — they must match byte-for-byte.
const WelcomeRU = `👋 Привет! Я бот для управления вашей подпиской. Вот что я умею:

⏰ Напоминания — предупрежу об окончании подписки за 7, 3 и 1 день.
💳 Продление — после оплаты нажмите «Я оплатил» под напоминанием, и администратор подтвердит продление.
👤 Личный кабинет — статус подписки, ссылка и подарки: /me.
🎁 Пробный период (/trial) — создать пробный профиль, если у вас ещё нет активной подписки.
🎁 Подарок (/gift) — подарить подписку человеку, у которого есть Telegram: он сам активирует подарок в этом боте.
➕ Приглашение (/invite) — оформить подписку тому, у кого нет Telegram или кто не может им пользоваться: вы получите готовую ссылку и передадите её сами.

❗️ Сначала привяжите свой Telegram к профилю подписки — без привязки я не узнаю, какая подписка ваша, и напоминания приходить не будут.

Как привязать аккаунт — по шагам:
1. Откройте приложение, в котором вы пользуетесь подпиской, и скопируйте ссылку на подписку.
2. Отправьте эту ссылку мне обычным сообщением в этот чат — команда /register не нужна.
3. Я найду ваш профиль и спрошу «Привязать ваш Telegram к профилю …?» — нажмите кнопку «Привязать».
4. Когда придёт сообщение «✅ Готово!», привязка завершена и напоминания включены.

Нет ссылки под рукой? Нажмите «🔗 Привязать аккаунт» ниже (или отправьте /register) и введите имя профиля (например, ivan_petrov).
Если ошиблись или передумали — отправьте /cancel и начните заново.

Все команды:
/me — личный кабинет: статус подписки, ссылка, подарки
/menu — открыть меню с кнопками
/trial — активировать пробный период (только для новых пользователей)
/register — привязать свой Telegram к профилю
/tariff — посмотреть текущие тарифы
/gift — подарить подписку (получателю с Telegram)
/mygifts — мои подарочные подписки и их статус
/invite — оформить подписку человеку без Telegram
/cancel — отменить текущее действие
/help — помощь`

const welcomeEN = `👋 Hi! I'm a bot for managing your subscription. Here's what I can do:

⏰ Reminders — I'll warn you 7, 3 and 1 day before your subscription expires.
💳 Renewal — after paying, tap “I paid” under the reminder and the administrator will confirm the renewal.
👤 My account — subscription status, link and gifts: /me.
🎁 Free trial (/trial) — create a trial profile if you do not have an active subscription yet.
🎁 Gift (/gift) — gift a subscription to someone who has Telegram: they activate the gift in this bot themselves.
➕ Invite (/invite) — get a subscription for someone who has no Telegram or can't use it: you'll receive a ready link to pass along yourself.

❗️ First, link your Telegram to your subscription profile — without the link I can't tell which subscription is yours, and no reminders will arrive.

How to link your account — step by step:
1. Open the app you use your subscription in and copy your subscription link.
2. Send that link to me as a regular message in this chat — no /register needed.
3. I'll find your profile and ask “Link your Telegram to profile …?” — tap the “Привязать” (Link) button.
4. When you receive the “✅ Done!” message, the link is complete and reminders are on.

Don't have the link handy? Tap “🔗 Link account” below (or send /register) and enter your profile name (for example, ivan_petrov).
If you make a mistake or change your mind — send /cancel and start over.

All commands:
/me — my account: subscription status, link, gifts
/menu — open the button menu
/trial — start the free trial (new users only)
/register — link your Telegram to a profile
/tariff — show current plans
/gift — gift a subscription (recipient has Telegram)
/mygifts — my gift subscriptions and their status
/invite — get a subscription for someone without Telegram
/cancel — cancel the current action
/help — help`
