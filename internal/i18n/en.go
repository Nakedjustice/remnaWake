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
	welcomeRU:             welcomeEN,

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
const welcomeRU = `⏰ Привет! Я бот-напоминалка: если ваша подписка на КВН скоро закончится, я сообщу об этом заранее — за 7, 3 или 1 день до окончания.

❗️ Чтобы получать уведомления, сначала привяжите свой Telegram к профилю подписки. Без привязки я не смогу понять, какая подписка ваша, и напоминания приходить не будут.

Как привязать:
1. Нажмите кнопку «🔗 Привязать аккаунт» ниже (или команду /register).
2. Введите имя вашего профиля — его можно посмотреть в приложении.
3. Подтвердите привязку — и всё готово, уведомления включены.

Меню и команды:
/me — личный кабинет: статус подписки, ссылка, подарки
/menu — открыть меню с кнопками
/register — привязать свой Telegram к профилю
/tariff — посмотреть текущие тарифы
/gift — подарить подписку
/mygifts — мои подарочные подписки и их статус
/cancel — отменить текущее действие

После оплаты нажмите «Я оплатил» — администратор получит уведомление и подтвердит продление.`

const welcomeEN = `⏰ Hi! I'm a reminder bot: if your subscription is about to end, I'll let you know in advance — 7, 3 and 1 day before it expires.

❗️ To receive notifications, first link your Telegram to your subscription profile. Without the link I can't tell which subscription is yours, and no reminders will arrive.

How to link:
1. Tap the “🔗 Link account” button below (or use /register).
2. Enter your profile name — you can find it in the app.
3. Confirm the link — that's it, notifications are on.

Menu and commands:
/me — my account: subscription status, link, gifts
/menu — open the button menu
/register — link your Telegram to a profile
/tariff — show current plans
/gift — gift a subscription
/mygifts — my gift subscriptions and their status
/cancel — cancel the current action

After paying, tap “I paid” — the administrator will be notified and confirm the renewal.`
