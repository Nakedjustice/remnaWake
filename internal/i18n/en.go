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
	"Отменить текущее действие":         "Cancel the current action",
	"Помощь": "Help",
	"О боте": "About the bot",
	"Панель администратора": "Admin panel",
	"Статистика":            "Statistics",
}

// The welcome message is kept as named constants so the map stays readable.
// WelcomeRU is exported so the telegram package can reference the same string
// that serves as the translation key — they must match byte-for-byte.
const WelcomeRU = `👋 Привет! Я бот для управления вашей подпиской. Вот что я умею:

⏰ Напоминания — предупрежу об окончании подписки за 7, 3 и 1 день.
💳 Продление — после оплаты нажмите «Я оплатил» под напоминанием, и администратор подтвердит продление.
👤 Личный кабинет — статус подписки, ссылка и подарки: /me.
🎁 Подарок (/gift) — подарить подписку человеку, у которого есть Telegram: он сам активирует подарок в этом боте.
➕ Приглашение (/invite) — оформить подписку тому, у кого нет Telegram или кто не может им пользоваться: вы получите готовую ссылку и передадите её сами.

❗️ Сначала привяжите свой Telegram к профилю подписки — без привязки я не узнаю, какая подписка ваша, и напоминания приходить не будут.

Как привязать аккаунт — по шагам:
1. Нажмите кнопку «🔗 Привязать аккаунт» под этим сообщением (или отправьте команду /register).
2. Бот попросит ввести имя вашего профиля. Откройте приложение, в котором вы пользуетесь подпиской, — имя профиля написано там (например, ivan_petrov).
3. Наберите это имя в чате обычным сообщением и отправьте — точно так, как оно написано в приложении.
4. Бот спросит «Привязать ваш Telegram к профилю …?» — нажмите кнопку «Привязать».
5. Когда придёт сообщение «✅ Готово!», привязка завершена и напоминания включены.

Если ошиблись или передумали — отправьте /cancel и начните заново.

Все команды:
/me — личный кабинет: статус подписки, ссылка, подарки
/menu — открыть меню с кнопками
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
🎁 Gift (/gift) — gift a subscription to someone who has Telegram: they activate the gift in this bot themselves.
➕ Invite (/invite) — get a subscription for someone who has no Telegram or can't use it: you'll receive a ready link to pass along yourself.

❗️ First, link your Telegram to your subscription profile — without the link I can't tell which subscription is yours, and no reminders will arrive.

How to link your account — step by step:
1. Tap the “🔗 Link account” button under this message (or send the /register command).
2. The bot will ask for your profile name. Open the app you use your subscription in — the profile name is shown there (for example, ivan_petrov).
3. Type that name in the chat as a regular message and send it — exactly as it appears in the app.
4. The bot will ask “Link your Telegram to profile …?” — tap the “Привязать” (Link) button.
5. When you receive the “✅ Done!” message, the link is complete and reminders are on.

If you make a mistake or change your mind — send /cancel and start over.

All commands:
/me — my account: subscription status, link, gifts
/menu — open the button menu
/register — link your Telegram to a profile
/tariff — show current plans
/gift — gift a subscription (recipient has Telegram)
/mygifts — my gift subscriptions and their status
/invite — get a subscription for someone without Telegram
/cancel — cancel the current action
/help — help`
