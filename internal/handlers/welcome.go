package handlers

import "github.com/gofiber/fiber/v2"

func Welcome(c *fiber.Ctx) error {
	return c.Render("Welcome to the chat!", nil, "layouts/main")
}
